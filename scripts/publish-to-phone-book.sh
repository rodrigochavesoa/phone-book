#!/usr/bin/env bash
# Publica este diretório em https://github.com/rodrigochavesoa/phone-book
# (repositório separado do monorepo into_to_algorithms).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

REMOTE_URL="https://github.com/rodrigochavesoa/phone-book.git"
TOP="$(git rev-parse --show-toplevel 2>/dev/null || true)"

if [[ "$TOP" != "$ROOT" ]]; then
  echo "Inicializando repositório Git só em phone_book (não use o git do monorepo aqui)."
  git init -b main
fi

if git check-ignore -q generate_names/db/bigon_bookX.db; then
  echo "OK: generate_names/db/bigon_bookX.db está no .gitignore"
else
  echo "AVISO: bigon_bookX.db não está ignorado — revise o .gitignore antes do push."
fi

git add -A
git status

if git diff --cached --quiet; then
  echo "Nada novo para commitar."
else
  git commit -m "$(cat <<'EOF'
Publica phone-book como repositório standalone.

Inclui app Go, web UI e documentação; ignora o SQLite grande.
EOF
)"
fi

if git remote get-url origin &>/dev/null; then
  CURRENT="$(git remote get-url origin)"
  if [[ "$CURRENT" != *"phone-book"* ]]; then
    echo "Remote origin aponta para: $CURRENT"
    echo "Use: git remote set-url origin $REMOTE_URL"
    echo "Ou:  git remote add phone-book $REMOTE_URL && git push -u phone-book main"
    exit 1
  fi
else
  git remote add origin "$REMOTE_URL"
fi

echo "Enviando para $REMOTE_URL ..."
git push -u origin main
