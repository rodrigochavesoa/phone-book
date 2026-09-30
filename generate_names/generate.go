package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

const (
	defaultDBFile = "generate_names/db/bigon_bookV.db"
	batchSize     = int64(40_320)
)

var db *sql.DB
var sinalInterrupcao chan os.Signal

func main() {
	dbPath := flag.String("db", defaultDBFile, "caminho para o banco SQLite")
	flag.Parse()

	n := 5 // Para 9, o total de combinações é 9! = 362880.

	var err error
	// Assegura que a pasta do DB existe
	err = os.MkdirAll(filepath.Dir(*dbPath), 0755)
	if err != nil {
		log.Fatalf("Erro criando pasta do DB: %v", err)
	}

	db, err = sql.Open("sqlite", *dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS progresso (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		num_combinacao BIGINT NOT NULL UNIQUE,
		combinacao TEXT NOT NULL,
		nome TEXT NOT NULL,
		middle TEXT NOT NULL,
		sobrenome TEXT NOT NULL
	);`)
	if err != nil {
		log.Fatalf("Erro ao iniciar o banco: %v", err)
	}
	if err := ensureSearchIndexes(db); err != nil {
		log.Fatalf("Erro ao criar índices de busca: %v", err)
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS execucoes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		start_time TEXT NOT NULL,
		end_time TEXT,
		duration_minutes REAL,
		total_combinacoes BIGINT,
		status TEXT NOT NULL
	);`)
	if err != nil {
		log.Fatalf("Erro ao criar tabela de execucoes: %v", err)
	}

	var últimoNum int64 = 0
	var últimaString string = ""
	_ = db.QueryRow("SELECT num_combinacao, combinacao FROM progresso ORDER BY num_combinacao DESC LIMIT 1").Scan(&últimoNum, &últimaString)

	arr := make([]int, n)
	totalCombinacoesEncontradas := últimoNum

	// listas simples de nomes, middle initials e sobrenomes (expandir conforme necessário)
	firstNames := []string{"Ana", "Bruno", "Carlos", "Diana", "Eduardo", "Fabiana", "Gustavo", "Helena", "Igor", "Juliana"}
	lastNames := []string{"Silva", "Santos", "Oliveira", "Souza", "Pereira", "Lima", "Gomes", "Ribeiro", "Ferreira", "Almeida"}
	middles := []string{"A.", "B.", "C.", "D.", "E.", "F.", "G.", "H.", "I.", "J."} // iniciais reduzidas

	// alvo desejado de nomes únicos
	const desiredUniqueNames int64 = 3_628_800
	maxCombinations := factorial(n)
	if desiredUniqueNames > maxCombinations {
		log.Printf("alvo de %d nomes é maior que as %d permutações possíveis para N=%d", desiredUniqueNames, maxCombinations, n)
	}

	// verificar capacidade F*L*M e expandir middles dinamicamente se necessário
	F := int64(len(firstNames))
	L := int64(len(lastNames))
	M := int64(len(middles))
	capacity := F * L * M
	if capacity < desiredUniqueNames {
		// calcular M necessário
		needM := (desiredUniqueNames + (F * L) - 1) / (F * L) // ceil(desired/(F*L))
		// expandir middles até needM (gera A., B., ..., Z., AA., AB., ...)
		middles = expandMiddles(middles, needM)
		M = int64(len(middles))
		capacity = F * L * M
		if capacity < desiredUniqueNames {
			log.Fatalf("Ainda incapaz de alcançar desired %d mesmo após expandir middles; capacidade=%d", desiredUniqueNames, capacity)
		}
		log.Printf("Expandido middles para %d entradas; nova capacidade=%d", len(middles), capacity)
	}

	if últimoNum >= desiredUniqueNames {
		fmt.Printf("🎉 Meta de %d registros já foi atingida.\n", desiredUniqueNames)
		return
	}

	if últimoNum > 0 {
		fmt.Printf("📦 Checkpoint encontrado! Retomando a partir da combinação nº %d\n", últimoNum)
		arr = stringToSlice(últimaString)
		if len(arr) != n {
			log.Fatalf("checkpoint possui permutação de tamanho %d, mas N=%d", len(arr), n)
		}
		temProxima := proximaPermutacao(arr)
		if !temProxima {
			fmt.Println("🎉 Todas as combinações possíveis já foram processadas e salvas!")
			return
		}
	} else {
		for i := range arr {
			arr[i] = i
		}
	}

	sinalInterrupcao = make(chan os.Signal, 1)
	signal.Notify(sinalInterrupcao, os.Interrupt, syscall.SIGTERM)

	var startTime time.Time
	startTime = time.Now()

	var runID int64 = 0

	fmt.Printf("🚀 Iniciando algoritmo resiliente para N=%d...\n", n)
	fmt.Println("------------------------------------------------------------------")

	// Registra execução no banco
	res, err := db.Exec("INSERT INTO execucoes (start_time, status) VALUES (?, ?)", startTime.Format(time.RFC3339), "running")
	if err != nil {
		log.Printf("Aviso: não foi possível registrar execução no DB: %v", err)
	} else {
		runID, _ = res.LastInsertId()
	}

	tx, stmt, err := beginInsertBatch(db)
	if err != nil {
		log.Fatalf("Erro ao iniciar lote de inserção: %v", err)
	}
	batchRows := int64(0)

	// A meta é total, não adicional ao checkpoint anterior.
	for totalCombinacoesEncontradas < desiredUniqueNames {
		select {
		case <-sinalInterrupcao:
			if batchRows > 0 {
				if err := commitInsertBatch(tx, stmt); err != nil {
					log.Fatalf("Erro ao confirmar checkpoint interrompido: %v", err)
				}
			}
			endTime := time.Now()
			dur := endTime.Sub(startTime)
			fmt.Printf("\n🛑 Interrupção detectada! Checkpoint salvo no banco: nº %d\n", totalCombinacoesEncontradas)
			fmt.Printf("Hora de início: %s\n", startTime.Format("2006-01-02 15:04:05"))
			fmt.Printf("Hora de término: %s\n", endTime.Format("2006-01-02 15:04:05"))
			fmt.Printf("Duração: %.2f minutos\n", dur.Minutes())
			if runID != 0 {
				_, _ = db.Exec("UPDATE execucoes SET end_time=?, duration_minutes=?, total_combinacoes=?, status=? WHERE id=?", endTime.Format(time.RFC3339), dur.Minutes(), totalCombinacoesEncontradas, "interrupted", runID)
			}
			return
		default:
		}

		totalCombinacoesEncontradas++
		arrString := sliceToString(arr)
		idx := totalCombinacoesEncontradas - 1
		firstName := firstNames[int(idx%F)]
		lastName := lastNames[int((idx/F)%L)]
		middle := middles[int((idx/(F*L))%M)]

		if _, err := stmt.Exec(totalCombinacoesEncontradas, arrString, firstName, middle, lastName); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			log.Fatalf("Erro ao salvar combinação nº %d: %v", totalCombinacoesEncontradas, err)
		}
		batchRows++

		if batchRows == batchSize {
			if err := commitInsertBatch(tx, stmt); err != nil {
				log.Fatalf("Erro ao confirmar checkpoint nº %d: %v", totalCombinacoesEncontradas, err)
			}
			fmt.Printf("📦 Checkpoint salvo: %d registros\n", totalCombinacoesEncontradas)
			batchRows = 0
			if totalCombinacoesEncontradas < desiredUniqueNames {
				tx, stmt, err = beginInsertBatch(db)
				if err != nil {
					log.Fatalf("Erro ao iniciar próximo lote: %v", err)
				}
			}
		}

		if !proximaPermutacao(arr) {
			break
		}
	}
	if batchRows > 0 {
		if err := commitInsertBatch(tx, stmt); err != nil {
			log.Fatalf("Erro ao confirmar lote final: %v", err)
		}
		fmt.Printf("📦 Checkpoint final salvo: %d registros\n", totalCombinacoesEncontradas)
	}

	fmt.Println("------------------------------------------------------------------")
	fmt.Printf("🎉 Concluído com sucesso! Total de combinações salvas no histórico: %d\n", totalCombinacoesEncontradas)
	endTime := time.Now()
	dur := endTime.Sub(startTime)
	fmt.Printf("Hora de início: %s\n", startTime.Format("2006-01-02 15:04:05"))
	fmt.Printf("Hora de término: %s\n", endTime.Format("2006-01-02 15:04:05"))
	fmt.Printf("Duração: %.2f minutos\n", dur.Minutes())
	if runID != 0 {
		_, _ = db.Exec("UPDATE execucoes SET end_time=?, duration_minutes=?, total_combinacoes=?, status=? WHERE id=?", endTime.Format(time.RFC3339), dur.Minutes(), totalCombinacoesEncontradas, "completed", runID)
	}
}

func beginInsertBatch(db *sql.DB) (*sql.Tx, *sql.Stmt, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, nil, err
	}
	stmt, err := tx.Prepare("INSERT INTO progresso (num_combinacao, combinacao, nome, middle, sobrenome) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		_ = tx.Rollback()
		return nil, nil, err
	}
	return tx, stmt, nil
}

func commitInsertBatch(tx *sql.Tx, stmt *sql.Stmt) error {
	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func ensureSearchIndexes(db *sql.DB) error {
	statements := []string{
		"CREATE INDEX IF NOT EXISTS idx_progresso_nome_nocase ON progresso(nome COLLATE NOCASE)",
		"CREATE INDEX IF NOT EXISTS idx_progresso_middle_nocase ON progresso(middle COLLATE NOCASE)",
		"CREATE INDEX IF NOT EXISTS idx_progresso_sobrenome_nocase ON progresso(sobrenome COLLATE NOCASE)",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_progresso_nome_completo_nocase ON progresso(nome COLLATE NOCASE, middle COLLATE NOCASE, sobrenome COLLATE NOCASE)",
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func factorial(n int) int64 {
	result := int64(1)
	for i := 2; i <= n; i++ {
		result *= int64(i)
	}
	return result
}

// expandMiddles expande a slice inicial de middles para ter pelo menos targetM elementos.
// Gera rótulos como A., B., ..., Z., AA., AB., ... usando letras A-Z.
func expandMiddles(base []string, targetM int64) []string {
	letters := []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	out := make([]string, 0, targetM)
	seen := make(map[string]struct{}, targetM)
	for _, v := range base {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
		if int64(len(out)) >= targetM {
			return out
		}
	}

	// gerar sequências de comprimento crescente
	length := 1
	for int64(len(out)) < targetM {
		// generate all combinations of given length
		indexes := make([]int, length)
		for {
			// build string
			var b strings.Builder
			for i := 0; i < length; i++ {
				b.WriteRune(letters[indexes[i]])
			}
			b.WriteString(".")
			candidate := b.String()
			if _, ok := seen[candidate]; !ok {
				seen[candidate] = struct{}{}
				out = append(out, candidate)
			}
			if int64(len(out)) >= targetM {
				return out
			}
			// increment indexes
			pos := length - 1
			for pos >= 0 {
				indexes[pos]++
				if indexes[pos] < len(letters) {
					break
				}
				indexes[pos] = 0
				pos--
			}
			if pos < 0 {
				break // exhausted this length
			}
		}
		length++
	}
	return out
}

// Algoritmo de Narayana Pandita A decisão foi abandonar a recursão
// (onde a função chama a si mesma) e adotar uma abordagem iterativa (baseada em um laço de repetição contínuo).
func proximaPermutacao(arr []int) bool {
	i := len(arr) - 2
	for i >= 0 && arr[i] >= arr[i+1] {
		i--
	}
	if i < 0 {
		return false // Chegou na última combinação possível.
	}

	j := len(arr) - 1
	for arr[j] <= arr[i] {
		j--
	}

	// Troca os elementos i e j
	arr[i], arr[j] = arr[j], arr[i]

	// Inverte o restante da sequência após a posição i
	left, right := i+1, len(arr)-1
	for left < right {
		arr[left], arr[right] = arr[right], arr[left]
		left++
		right--
	}
	return true
}

func sliceToString(arr []int) string {
	strVals := make([]string, len(arr))
	for i, v := range arr {
		strVals[i] = strconv.Itoa(v)
	}
	return strings.Join(strVals, " ")
}

func stringToSlice(str string) []int {
	fields := strings.Fields(str)
	arr := make([]int, len(fields))
	for i, f := range fields {
		arr[i], _ = strconv.Atoi(f)
	}
	return arr
}
