# Phone Book — lista telefônica com busca

Aplicação Go com interface web em `web/`. A busca usa um banco **SQLite** local.

Este repositório é a versão **standalone** do módulo `phone_book`, extraído do estudo de algoritmos em Go do projeto **[Introdução aos Estudos de Algoritmos (Golang)](https://github.com/rodrigochavesoa/into_to_algorithms)**. Lá você encontra o contexto completo da jornada de estudo, metodologia, referências (CLRS, Bhargava, MIT, CS50) e os demais exercícios — veja o [README principal do monorepo](https://github.com/rodrigochavesoa/into_to_algorithms/blob/main/README.md).

Repositório público deste app: [github.com/rodrigochavesoa/phone-book](https://github.com/rodrigochavesoa/phone-book).

## Pré-requisitos

- [Go](https://go.dev/dl/) instalado (o projeto usa Go 1.26+)
- Arquivo SQLite em `generate_names/db/` (veja [Banco de dados](#banco-de-dados) abaixo)

## Banco de dados

| Arquivo | No Git? | Uso |
|---------|---------|-----|
| `generate_names/db/bigon_bookV.db` | Sim (pequeno) | Testes e primeiros passos |
| `generate_names/db/bigon_bookX.db` | **Não** (muito grande) | Banco completo para busca em escala |

Para gerar o `bigon_bookX.db` localmente:

```bash
go run ./generate_names -db generate_names/db/bigon_bookX.db
```

O processo pode demorar; interrompa com `Ctrl+C` se quiser — a geração retoma do último registro salvo.

## Subir o servidor web

1. Clone este repositório e entre na pasta:

   ```bash
   git clone https://github.com/rodrigochavesoa/phone-book.git
   cd phone-book
   ```

2. Baixe as dependências:

   ```bash
   go mod download
   ```

3. Inicie o servidor (use `bookV` se ainda não gerou o `bookX`):

   ```bash
   go run . -server -port 8080 -db generate_names/db/bigon_bookX.db
   ```

   Para testes rápidos:

   ```bash
   go run . -server -port 8080 -db generate_names/db/bigon_bookV.db
   ```

4. Abra no navegador:

   **http://localhost:8080**

   Não abra `web/index.html` direto pelo explorador de arquivos (`file://`). A página precisa do servidor para chamar a API `/api/search`.

## Opções da linha de comando

| Flag | Padrão | Descrição |
|------|--------|-----------|
| `-server` | (desligado) | Ativa o modo servidor web |
| `-port` | `8080` | Porta HTTP |
| `-db` | `generate_names/db/bigon_bookX.db` | Caminho do arquivo SQLite |

Rode os comandos **na raiz deste repositório** (clone de `phone-book`).

O código também existe como pasta `phone_book/` no monorepo [into_to_algorithms](https://github.com/rodrigochavesoa/into_to_algorithms). O repositório **[phone-book](https://github.com/rodrigochavesoa/phone-book)** é a cópia **standalone** (raiz = este projeto, sem o prefixo `phone_book/`).

## Testar a API

```bash
curl "http://localhost:8080/api/search?q=Carlos"
```

## Modo terminal (sem interface web)

Sem `-server`, o programa faz uma única busca no terminal:

```bash
go run . -db generate_names/db/bigon_bookV.db
```

Digite o termo quando aparecer o prompt `>`.

## Problemas comuns

| Sintoma | O que fazer |
|--------|-------------|
| `banco de dados ... inacessível` | Confira o caminho em `-db` e se o arquivo `.db` existe |
| Busca não funciona na página | Suba com `-server` e use `http://localhost:8080` |
| Porta em uso | Troque a porta, ex.: `-port 8081` |

## Mais detalhes

Requisitos de produção e segurança: [docs/producao-seguranca-spec.md](docs/producao-seguranca-spec.md).

## Autor

**[Rodrigo Chaves](https://github.com/rodrigochavesoa)** — parte do ecossistema [Colab Developer](https://rodrigochavesoa.github.io/Colab_Developer/) / [Ponto Chave Design](https://www.instagram.com/pontochavedesign/).
