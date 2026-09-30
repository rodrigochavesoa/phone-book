package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	_ "modernc.org/sqlite"
)

const (
	defaultDBFile  = "generate_names/db/bigon_bookX.db"
	maxInputBytes  = 4 << 10
	maxQueryBytes  = 200
	maxSearchTerms = 5
)

var errInvalidSearchInput = errors.New("termo de busca contém caracteres inválidos")

const invalidSearchMessage = "Formato de busca inválido. Busque por ID, nome, sobrenome ou nome completo. Exemplos: 99, Carlos, Almeida, Ana B, Ana B Almeida."

type serverConfig struct {
	rateLimitRequests int
	rateLimitWindow   time.Duration
	searchTimeout     time.Duration
	cacheTTL          time.Duration
	cacheMaxEntries   int
}

var defaultServerConfig = serverConfig{
	rateLimitRequests: 20,
	rateLimitWindow:   10 * time.Second,
	searchTimeout:     2 * time.Second,
	cacheTTL:          30 * time.Second,
	cacheMaxEntries:   256,
}

// Record representa um registro da tabela progresso.
type Record struct {
	CombinationNum int64          `json:"combination_num"`
	Combination    string         `json:"combination"`
	FirstName      string         `json:"first_name"`
	MiddleName     sql.NullString `json:"-"`
	LastName       string         `json:"last_name"`
}

// MarshalJSON customiza a serialização do Record tratando MiddleName como string comum.
func (r Record) MarshalJSON() ([]byte, error) {
	type Alias Record
	return json.Marshal(&struct {
		Alias
		MiddleName string `json:"middle_name"`
	}{
		Alias:      (Alias)(r),
		MiddleName: r.MiddleName.String,
	})
}

func (r Record) String() string {
	if r.MiddleName.Valid && r.MiddleName.String != "" {
		return fmt.Sprintf("%d: [%s] -> %s %s %s", r.CombinationNum, r.Combination, r.FirstName, r.MiddleName.String, r.LastName)
	}
	return fmt.Sprintf("%d: [%s] -> %s %s", r.CombinationNum, r.Combination, r.FirstName, r.LastName)
}

func main() {
	dbPath := flag.String("db", defaultDBFile, "caminho para o banco SQLite")
	server := flag.Bool("server", false, "iniciar em modo servidor web")
	port := flag.String("port", "8080", "porta do servidor web")
	flag.Parse()

	db, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		log.Fatalf("erro ao abrir banco de dados %q: %v", *dbPath, err)
	}
	defer db.Close()

	// Configurações do pool de conexões para o SQLite.
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	if err := db.PingContext(context.Background()); err != nil {
		log.Fatalf("banco de dados %q inacessível: %v", *dbPath, err)
	}
	if err := ensureSearchIndexes(db); err != nil {
		log.Fatalf("erro ao preparar índices de busca: %v", err)
	}

	if *server {
		startServer(db, *port)
		return
	}

	// Execução em modo linha de comando.
	fmt.Println("Busca no banco de combinações (Modo CLI)")
	fmt.Println("Digite um id, nome e/ou sobrenome:")
	fmt.Print("> ")

	var input string
	var buf [maxInputBytes]byte
	n, err := os.Stdin.Read(buf[:])
	if err != nil && err != io.EOF {
		log.Fatalf("erro ao ler entrada: %v", err)
	}
	input = strings.TrimSpace(string(buf[:n]))
	if input == "" {
		fmt.Println("entrada vazia")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	records, err := search(ctx, db, input)
	if err != nil {
		log.Fatalf("erro durante a busca: %v", err)
	}

	if len(records) == 0 {
		fmt.Println("nenhum resultado encontrado")
		return
	}

	for _, r := range records {
		fmt.Println(r.String())
	}
}

// search seleciona a estratégia de busca de acordo com o formato da entrada.
func search(ctx context.Context, db *sql.DB, input string) ([]Record, error) {
	if len(input) > maxInputBytes {
		return nil, fmt.Errorf("entrada excede o limite de tamanho permitido")
	}
	if !isValidSearchInput(input) {
		return nil, errInvalidSearchInput
	}
	if len(strings.Fields(input)) > maxSearchTerms {
		return nil, fmt.Errorf("Use no máximo %d termos na busca.", maxSearchTerms)
	}

	switch {
	case isAllDigits(input):
		num, err := strconv.ParseInt(input, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("id inválido: %w", err)
		}
		return byExactID(ctx, db, num)
	default:
		return byName(ctx, db, input)
	}
}

func byExactID(ctx context.Context, db *sql.DB, num int64) ([]Record, error) {
	const q = `SELECT num_combinacao, combinacao, nome, middle, sobrenome FROM progresso WHERE num_combinacao = ?`
	var r Record
	err := db.QueryRowContext(ctx, q, num).Scan(&r.CombinationNum, &r.Combination, &r.FirstName, &r.MiddleName, &r.LastName)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("busca por id: %w", err)
	}
	return []Record{r}, nil
}

// byName busca registros associando os termos da entrada a nome, inicial/nome do meio e sobrenome.
func byName(ctx context.Context, db *sql.DB, input string) ([]Record, error) {
	terms := strings.Fields(input)
	if len(terms) > maxSearchTerms {
		return nil, fmt.Errorf("Use no máximo %d termos na busca.", maxSearchTerms)
	}

	const limit = ` LIMIT 200`

	switch len(terms) {
	case 1:
		return bySingleNameTerm(ctx, db, terms[0], 200)
	case 2:
		const selectPrefix = `SELECT num_combinacao, combinacao, nome, middle, sobrenome
			FROM progresso
			WHERE `
		column := "sobrenome"
		secondTerm := terms[1]
		if isMiddleInitial(secondTerm) {
			column = "middle"
			secondTerm = normalizeMiddleInitial(secondTerm)
		}
		if column == "middle" {
			query := selectPrefix + "nome = ? COLLATE NOCASE AND middle = ? COLLATE NOCASE" + limit
			return runQuery(ctx, db, query, terms[0], secondTerm)
		}
		query := selectPrefix + "nome = ? COLLATE NOCASE AND sobrenome LIKE ? ESCAPE '\\'" + limit
		return runQuery(ctx, db, query, terms[0], likePrefix(terms[1]))
	default:
		const selectPrefix = `SELECT num_combinacao, combinacao, nome, middle, sobrenome
			FROM progresso
			WHERE `
		lastName := strings.Join(terms[2:], " ")
		middle := normalizeMiddleInitial(terms[1])
		query := selectPrefix + `
			nome = ? COLLATE NOCASE
			AND middle = ? COLLATE NOCASE
			AND sobrenome LIKE ? ESCAPE '\'` + limit
		return runQuery(ctx, db, query, terms[0], middle, likePrefix(lastName))
	}
}

func bySingleNameTerm(ctx context.Context, db *sql.DB, term string, limit int) ([]Record, error) {
	queries := []string{
		`SELECT num_combinacao, combinacao, nome, middle, sobrenome
			FROM progresso
			WHERE nome = ? COLLATE NOCASE
			LIMIT ?`,
		`SELECT num_combinacao, combinacao, nome, middle, sobrenome
			FROM progresso
			WHERE middle = ? COLLATE NOCASE
			LIMIT ?`,
		`SELECT num_combinacao, combinacao, nome, middle, sobrenome
			FROM progresso
			WHERE sobrenome = ? COLLATE NOCASE
			LIMIT ?`,
	}

	seen := make(map[int64]struct{})
	records := make([]Record, 0, limit)
	for _, query := range queries {
		remaining := limit - len(records)
		if remaining <= 0 {
			break
		}
		next, err := runQuery(ctx, db, query, term, remaining)
		if err != nil {
			return nil, err
		}
		for _, record := range next {
			if _, ok := seen[record.CombinationNum]; ok {
				continue
			}
			seen[record.CombinationNum] = struct{}{}
			records = append(records, record)
			if len(records) == limit {
				break
			}
		}
	}
	return records, nil
}

func likePrefix(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s + "%"
}

func runQuery(ctx context.Context, db *sql.DB, query string, args ...any) ([]Record, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.CombinationNum, &r.Combination, &r.FirstName, &r.MiddleName, &r.LastName); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iteração do cursor: %w", err)
	}
	return records, nil
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isValidSearchInput(s string) bool {
	terms := strings.Fields(s)
	if len(terms) == 0 || len(terms) > maxSearchTerms {
		return false
	}

	if len(terms) == 1 {
		return isAllDigits(terms[0]) || isValidNameTerm(terms[0])
	}

	for _, term := range terms {
		if isAllDigits(term) {
			return false
		}
	}

	if !isValidNameTerm(terms[0]) {
		return false
	}
	if len(terms) >= 3 && !isMiddleInitial(terms[1]) {
		return false
	}
	for i, term := range terms[1:] {
		if i == 0 && isMiddleInitial(term) {
			continue
		}
		if !isValidNameTerm(term) {
			return false
		}
	}
	return true
}

func isValidNameTerm(term string) bool {
	if term == "" {
		return false
	}

	runes := []rune(term)
	if len(runes) < 3 {
		return false
	}
	for _, r := range runes {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

func isMiddleInitial(term string) bool {
	runes := []rune(term)
	if len(runes) == 1 {
		return unicode.IsLetter(runes[0])
	}
	return len(runes) == 2 && unicode.IsLetter(runes[0]) && runes[1] == '.'
}

func normalizeMiddleInitial(term string) string {
	runes := []rune(term)
	if len(runes) == 1 && unicode.IsLetter(runes[0]) {
		return string(runes[0]) + "."
	}
	return term
}

func ensureSearchIndexes(db *sql.DB) error {
	statements := []string{
		"CREATE INDEX IF NOT EXISTS idx_progresso_num_combinacao ON progresso(num_combinacao)",
		"CREATE INDEX IF NOT EXISTS idx_progresso_nome_nocase ON progresso(nome COLLATE NOCASE)",
		"CREATE INDEX IF NOT EXISTS idx_progresso_middle_nocase ON progresso(middle COLLATE NOCASE)",
		"CREATE INDEX IF NOT EXISTS idx_progresso_sobrenome_nocase ON progresso(sobrenome COLLATE NOCASE)",
		"CREATE INDEX IF NOT EXISTS idx_progresso_nome_completo_nocase ON progresso(nome COLLATE NOCASE, middle COLLATE NOCASE, sobrenome COLLATE NOCASE)",
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

// Servidor Web e Segurança

func startServer(db *sql.DB, port string) {
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum < 1 || portNum > 65535 {
		log.Fatalf("porta inválida %q: use um valor entre 1 e 65535", port)
	}

	mux := http.NewServeMux()

	limiter := newIPRateLimiter()
	limiter.startCleanup(1*time.Minute, 10*time.Second)
	cache := newSearchCache(defaultServerConfig.cacheTTL, defaultServerConfig.cacheMaxEntries)

	mux.Handle("/api/search", searchHandler(db, limiter, cache, defaultServerConfig))

	// Serve arquivos estáticos da interface web.
	webDir := "web"
	if _, err := os.Stat(webDir); os.IsNotExist(err) {
		webDir = filepath.Clean("phone_book/web")
	}
	fs := http.FileServer(http.Dir(webDir))
	mux.Handle("/", fs)

	handler := securityHeaders(mux)
	addr := ":" + port
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	fmt.Printf("Servidor iniciado em http://%s\n", validHostPort(port))
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("erro ao iniciar o servidor: %v", err)
	}
}

func searchHandler(db *sql.DB, limiter *ipRateLimiter, cache *searchCache, cfg serverConfig) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(map[string]string{"error": "Método não permitido"})
			return
		}

		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			ip = r.RemoteAddr
		}

		if !limiter.allow(ip, cfg.rateLimitRequests, cfg.rateLimitWindow) {
			log.Printf("rate_limit ip=%s path=%s", ip, r.URL.Path)
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]string{"error": "Muitas requisições. Por favor, aguarde um momento."})
			return
		}

		q := r.URL.Query().Get("q")
		q = strings.TrimSpace(q)

		if q == "" {
			log.Printf("bad_request ip=%s reason=empty_query", ip)
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Termo de busca vazio"})
			return
		}

		if len(q) > maxQueryBytes {
			log.Printf("bad_request ip=%s reason=query_too_long bytes=%d", ip, len(q))
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "Termo de busca muito longo"})
			return
		}

		cacheKey := normalizeCacheKey(q)
		if records, ok := cache.get(cacheKey); ok {
			json.NewEncoder(w).Encode(records)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), cfg.searchTimeout)
		defer cancel()

		records, err := search(ctx, db, q)
		if err != nil {
			if errors.Is(err, errInvalidSearchInput) {
				log.Printf("bad_request ip=%s reason=invalid_query", ip)
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": invalidSearchMessage})
				return
			}
			if errors.Is(err, context.Canceled) {
				log.Printf("request_canceled ip=%s", ip)
				return
			}
			if errors.Is(err, context.DeadlineExceeded) {
				log.Printf("search_timeout ip=%s", ip)
				w.WriteHeader(http.StatusServiceUnavailable)
				json.NewEncoder(w).Encode(map[string]string{"error": "Não foi possível concluir a busca agora. Tente um termo mais específico."})
				return
			}
			log.Printf("erro na busca: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Erro interno ao executar a busca"})
			return
		}

		if records == nil {
			records = []Record{}
		}

		cache.set(cacheKey, records)
		json.NewEncoder(w).Encode(records)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' https://fonts.googleapis.com; font-src https://fonts.gstatic.com; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		next.ServeHTTP(w, r)
	})
}

func validHostPort(port string) string {
	return net.JoinHostPort("localhost", port)
}

type cachedSearch struct {
	records   []Record
	expiresAt time.Time
}

type searchCache struct {
	items      map[string]cachedSearch
	ttl        time.Duration
	maxEntries int
	mu         sync.Mutex
}

func newSearchCache(ttl time.Duration, maxEntries int) *searchCache {
	return &searchCache{
		items:      make(map[string]cachedSearch),
		ttl:        ttl,
		maxEntries: maxEntries,
	}
}

func normalizeCacheKey(q string) string {
	return strings.ToLower(strings.Join(strings.Fields(q), " "))
}

func (c *searchCache) get(key string) ([]Record, bool) {
	if c == nil || c.ttl <= 0 || c.maxEntries <= 0 {
		return nil, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	item, ok := c.items[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(item.expiresAt) {
		delete(c.items, key)
		return nil, false
	}
	return cloneRecords(item.records), true
}

func (c *searchCache) set(key string, records []Record) {
	if c == nil || c.ttl <= 0 || c.maxEntries <= 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.items) >= c.maxEntries {
		c.evictExpiredOrOldest()
	}
	c.items[key] = cachedSearch{
		records:   cloneRecords(records),
		expiresAt: time.Now().Add(c.ttl),
	}
}

func (c *searchCache) evictExpiredOrOldest() {
	now := time.Now()
	var oldestKey string
	var oldestExpiry time.Time
	for key, item := range c.items {
		if now.After(item.expiresAt) {
			delete(c.items, key)
			return
		}
		if oldestKey == "" || item.expiresAt.Before(oldestExpiry) {
			oldestKey = key
			oldestExpiry = item.expiresAt
		}
	}
	if oldestKey != "" {
		delete(c.items, oldestKey)
	}
}

func cloneRecords(records []Record) []Record {
	if records == nil {
		return []Record{}
	}
	out := make([]Record, len(records))
	copy(out, records)
	return out
}

// ipRateLimiter gerencia a frequência de requisições por IP na memória.
type ipRateLimiter struct {
	ips map[string][]time.Time
	mu  sync.Mutex
}

func newIPRateLimiter() *ipRateLimiter {
	return &ipRateLimiter{ips: make(map[string][]time.Time)}
}

func (l *ipRateLimiter) allow(ip string, limit int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	times := l.ips[ip]

	var validTimes []time.Time
	for _, t := range times {
		if now.Sub(t) < window {
			validTimes = append(validTimes, t)
		}
	}

	if len(validTimes) >= limit {
		l.ips[ip] = validTimes
		return false
	}

	validTimes = append(validTimes, now)
	l.ips[ip] = validTimes
	return true
}

// startCleanup remove IPs inativos periodicamente para liberar memória.
func (l *ipRateLimiter) startCleanup(interval time.Duration, maxAge time.Duration) {
	go func() {
		for {
			time.Sleep(interval)
			l.mu.Lock()
			now := time.Now()
			for ip, times := range l.ips {
				var validTimes []time.Time
				for _, t := range times {
					if now.Sub(t) < maxAge {
						validTimes = append(validTimes, t)
					}
				}
				if len(validTimes) == 0 {
					delete(l.ips, ip)
				} else {
					l.ips[ip] = validTimes
				}
			}
			l.mu.Unlock()
		}
	}()
}
