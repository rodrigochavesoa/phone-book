package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestLikeEscapingEndToEnd(t *testing.T) {
	db := newTestDB(t)
	insertTestRecord(t, db, 3, "ana-c-silva", "Ana", "C.")
	insertTestRecord(t, db, 4, "diana-c-silva", "Diana", "C.")
	insertTestRecord(t, db, 5, "ana-ac-silva", "Ana", "AC.")
	insertTestRecord(t, db, 6, "ana-c-santos", "Ana", "C.")
	insertTestRecord(t, db, 7, "ana-c-lima", "Ana", "C.")
	setTestLastName(t, db, 3, "Silva")
	setTestLastName(t, db, 4, "Silva")
	setTestLastName(t, db, 5, "Silva")
	setTestLastName(t, db, 6, "Santos")
	setTestLastName(t, db, 7, "Lima")

	tests := []struct {
		name string
		run  func(context.Context, *sql.DB, string) ([]Record, error)
		in   string
		want string
		not  string
	}{
		{"nome e middle exatos", byName, "Ana C.", "3: [ana-c-silva]", "4: [diana-c-silva]"},
		{"nome e middle sem ponto", byName, "Ana C", "3: [ana-c-silva]", "4: [diana-c-silva]"},
		{"nome middle e sobrenome exatos", byName, "Ana C. Silva", "3: [ana-c-silva]", "5: [ana-ac-silva]"},
		{"nome middle sem ponto e sobrenome", byName, "Ana C Silva", "3: [ana-c-silva]", "5: [ana-ac-silva]"},
		{"prefixo do sobrenome", byName, "Ana C. L", "7: [ana-c-lima]", "4: [diana-c-silva]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records, err := tt.run(context.Background(), db, tt.in)
			if err != nil {
				t.Fatal(err)
			}
			var gotBuf strings.Builder
			for _, r := range records {
				gotBuf.WriteString(r.String())
				gotBuf.WriteString("\n")
			}
			got := gotBuf.String()
			if !strings.Contains(got, tt.want) {
				t.Fatalf("resultado literal ausente para %q:\n%s", tt.in, got)
			}
			if strings.Contains(got, tt.not) {
				t.Fatalf("resultado obtido via wildcard para %q:\n%s", tt.in, got)
			}
		})
	}
}

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("CREATE TABLE progresso (" +
		"num_combinacao INTEGER NOT NULL, combinacao TEXT NOT NULL, " +
		"nome TEXT, middle TEXT, sobrenome TEXT)"); err != nil {
		t.Fatal(err)
	}
	return db
}

func insertTestRecord(t *testing.T, db *sql.DB, id int, combination, name, middle string) {
	t.Helper()
	if _, err := db.Exec(
		"INSERT INTO progresso (num_combinacao, combinacao, nome, middle, sobrenome) VALUES (?, ?, ?, ?, '')",
		id, combination, name, middle,
	); err != nil {
		t.Fatal(err)
	}
}

func setTestLastName(t *testing.T, db *sql.DB, id int, lastName string) {
	t.Helper()
	if _, err := db.Exec("UPDATE progresso SET sobrenome = ? WHERE num_combinacao = ?", lastName, id); err != nil {
		t.Fatal(err)
	}
}

func TestByExactID(t *testing.T) {
	db := newTestDB(t)
	insertTestRecord(t, db, 1, "1", "Ana", "")

	tests := []struct {
		id   int64
		want string
	}{
		{1, "1: [1] -> Ana"},
		{999, ""},
	}
	for _, tt := range tests {
		records, err := byExactID(context.Background(), db, tt.id)
		if err != nil {
			t.Fatal(err)
		}
		var got string
		if len(records) > 0 {
			got = records[0].String()
		}
		if tt.want != "" && !strings.Contains(got, tt.want) {
			t.Fatalf("saída para id %d = %q; esperava conter %q", tt.id, got, tt.want)
		}
		if tt.want == "" && got != "" {
			t.Fatalf("saída para id %d = %q; esperava vazio", tt.id, got)
		}
	}
}

func TestByNameRejectsExcessSearchTerms(t *testing.T) {
	db := newTestDB(t)
	input := strings.Repeat("termo ", maxSearchTerms) + "extra"

	_, err := byName(context.Background(), db, input)
	if err == nil {
		t.Fatal("esperava erro de excesso de termos")
	}
	want := "Use no máximo 5 termos na busca."
	if err.Error() != want {
		t.Fatalf("erro = %q; esperava %q", err.Error(), want)
	}
}

func TestSearchRouting(t *testing.T) {
	db := newTestDB(t)
	insertTestRecord(t, db, 1, "0 1 2", "Ana", "C.")
	setTestLastName(t, db, 1, "Silva")

	// 1. Mixed query "4500í" is not a valid search format.
	_, err := search(context.Background(), db, "4500í")
	if err != errInvalidSearchInput {
		t.Fatalf("search(4500í) error = %v; expected %v", err, errInvalidSearchInput)
	}

	// 2. Pure name "Ana" should route to byName and find the record
	records, err := search(context.Background(), db, "Ana")
	if err != nil {
		t.Fatalf("search(Ana) failed: %v", err)
	}
	if len(records) != 1 || records[0].FirstName != "Ana" {
		t.Errorf("expected Ana, got %v", records)
	}

	// 3. Numeric snippets with spaces are invalid in the public search.
	for _, q := range []string{"0 1", "0 1 2 3", "1 2 999"} {
		_, err = search(context.Background(), db, q)
		if err != errInvalidSearchInput {
			t.Fatalf("search(%q) error = %v; expected %v", q, err, errInvalidSearchInput)
		}
	}

	// 4. SQL wildcard characters are invalid user input for this search UI.
	for _, q := range []string{"_43", "_43%"} {
		_, err = search(context.Background(), db, q)
		if err != errInvalidSearchInput {
			t.Fatalf("search(%q) error = %v; expected %v", q, err, errInvalidSearchInput)
		}
	}
}

func TestSearchRejectsInvalidInput(t *testing.T) {
	db := newTestDB(t)

	for _, q := range []string{"99$", "Almeida.", "-Ana", "almeida-", "fd", "%Ana%", "Ana123", "Ana fd", "0 1 2 3"} {
		_, err := search(context.Background(), db, q)
		if err == nil {
			t.Fatalf("esperava erro para termo inválido %q", q)
		}
		if err != errInvalidSearchInput {
			t.Fatalf("erro para %q = %v; esperava %v", q, err, errInvalidSearchInput)
		}
	}
}

func TestSearchHandlerReturnsBadRequestForInvalidInput(t *testing.T) {
	db := newTestDB(t)
	handler := searchHandler(db, newIPRateLimiter(), newSearchCache(time.Minute, 16), defaultServerConfig)

	for _, path := range []string{
		"/api/search?q=99%24",
		"/api/search?q=Almeida.",
		"/api/search?q=-Ana",
		"/api/search?q=almeida-",
		"/api/search?q=fd",
		"/api/search?q=%25Ana%25",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "127.0.0.1:12345"
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d; esperava %d; body=%s", path, rec.Code, http.StatusBadRequest, rec.Body.String())
		}

		var body map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["error"] == "" || strings.Contains(strings.ToLower(body["error"]), "interno") {
			t.Fatalf("mensagem inadequada para entrada inválida: %q", body["error"])
		}
		if body["error"] != invalidSearchMessage {
			t.Fatalf("mensagem = %q; esperava %q", body["error"], invalidSearchMessage)
		}
	}
}

func TestSearchHandlerAppliesRateLimit(t *testing.T) {
	db := newTestDB(t)
	insertTestRecord(t, db, 1, "1", "Ana", "")
	cfg := defaultServerConfig
	cfg.rateLimitRequests = 1
	cfg.rateLimitWindow = time.Minute
	handler := searchHandler(db, newIPRateLimiter(), newSearchCache(time.Minute, 16), cfg)

	for i, want := range []int{http.StatusOK, http.StatusTooManyRequests} {
		req := httptest.NewRequest(http.MethodGet, "/api/search?q=Ana", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != want {
			t.Fatalf("requisição %d status = %d; esperava %d; body=%s", i+1, rec.Code, want, rec.Body.String())
		}
	}
}

func TestSearchCacheNormalizesAndClonesResults(t *testing.T) {
	cache := newSearchCache(time.Minute, 16)
	key := normalizeCacheKey("  Ana   SILVA  ")
	cache.set(key, []Record{{CombinationNum: 1, Combination: "1", FirstName: "Ana", LastName: "Silva"}})

	records, ok := cache.get(normalizeCacheKey("ana silva"))
	if !ok {
		t.Fatal("esperava cache hit")
	}
	records[0].FirstName = "Alterado"

	records, ok = cache.get(key)
	if !ok {
		t.Fatal("esperava segundo cache hit")
	}
	if records[0].FirstName != "Ana" {
		t.Fatalf("cache retornou slice compartilhado: %q", records[0].FirstName)
	}
}
