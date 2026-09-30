# Especificacao de Producao e Seguranca - Phone Book

## 1. Objetivo

Definir os requisitos tecnicos para preparar o projeto `phone_book` para execucao em ambiente de producao, com foco em seguranca, disponibilidade, protecao do banco de dados, observabilidade e reducao de abuso na API de busca.

Esta especificacao complementa os controles ja implementados no backend Go, como validacao de entrada, rate limit, timeout de busca, cache em memoria, headers de seguranca e mensagens genericas para erros internos.

## 2. Escopo

Esta fase cobre a infraestrutura e os controles operacionais ao redor da aplicacao:

- Proxy reverso com HTTPS.
- Exposicao controlada de portas.
- Permissoes minimas para arquivos e banco SQLite.
- Backup criptografado e processo de restauracao.
- Logs centralizados e monitoramento.
- Autenticacao antes da busca, caso os dados sejam sensiveis.
- Procedimentos de validacao antes de liberar em producao.

Fora de escopo nesta fase:

- Redesenho da interface.
- Troca obrigatoria do SQLite por outro banco.
- Criacao de painel administrativo.
- Implementacao de multi-tenant.

## 3. Estado Atual da Aplicacao

O backend ja possui os seguintes controles:

- Validacao de entrada antes de consultar o banco.
- Rejeicao de caracteres inesperados em buscas.
- Limite de tamanho da query.
- Limite de termos na busca.
- Rate limit por IP.
- Timeout para consultas no banco.
- Cache curto para buscas repetidas.
- Headers HTTP de seguranca.
- Uso de queries parametrizadas.
- Mensagens genericas para erros internos.

A proxima equipe deve manter esses controles ativos e adicionar a camada operacional descrita abaixo.

## 4. Requisitos de Producao

### 4.1 Proxy Reverso com HTTPS

#### Requisito

A aplicacao Go nao deve ser exposta diretamente para a internet. Ela deve rodar atras de um proxy reverso, preferencialmente Nginx ou Caddy, com TLS/HTTPS obrigatorio.

#### Implementacao Recomendada

Opcoes aceitas:

- Caddy, recomendado pela simplicidade de HTTPS automatico.
- Nginx, recomendado quando a equipe ja possui padrao operacional com Nginx.

O proxy deve:

- Encaminhar trafego externo HTTPS para a aplicacao Go em `localhost:8080`.
- Redirecionar HTTP para HTTPS.
- Configurar certificados TLS validos.
- Definir limite de tamanho de request.
- Definir timeout de leitura e escrita.
- Preservar o IP real do cliente via headers como `X-Forwarded-For` e `X-Real-IP`.

#### Exemplo Conceitual com Nginx

```nginx
server {
    listen 80;
    server_name exemplo.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name exemplo.com;

    ssl_certificate /etc/letsencrypt/live/exemplo.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/exemplo.com/privkey.pem;

    client_max_body_size 16k;
    proxy_read_timeout 10s;
    proxy_send_timeout 10s;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

#### Criterios de Aceite

- A aplicacao nao responde publicamente por HTTP sem redirecionamento.
- O dominio responde via HTTPS com certificado valido.
- A porta da aplicacao Go nao fica exposta publicamente.
- Headers `X-Forwarded-*` chegam corretamente ao backend ou ao log do proxy.

## 5. Firewall e Exposicao de Portas

### 5.1 Requisito

Somente as portas necessarias devem estar acessiveis publicamente.

### 5.2 Regras Recomendadas

Liberar publicamente:

- `80/tcp`, apenas para redirecionamento HTTP para HTTPS.
- `443/tcp`, para acesso HTTPS.

Restrigir:

- `8080/tcp` deve aceitar conexoes apenas de `localhost`.
- SSH deve ser restrito por IP permitido, VPN ou bastion host.

### 5.3 Criterios de Aceite

- Scan externo nao deve mostrar a porta `8080` aberta.
- A aplicacao deve continuar acessivel via `443`.
- Acesso administrativo deve ser auditavel e restrito.

## 6. Permissoes Minimas do Banco de Dados

### 6.1 Requisito

O processo da aplicacao deve executar com usuario dedicado e permissao minima sobre arquivos.

### 6.2 Implementacao Recomendada

Criar usuario de sistema dedicado, por exemplo:

```bash
useradd --system --home /opt/phone_book --shell /usr/sbin/nologin phonebook
```

Permissoes recomendadas:

- O binario da aplicacao deve ser legivel e executavel pelo usuario `phonebook`.
- O banco SQLite deve ser legivel e gravavel somente se a aplicacao precisar escrever.
- Se a aplicacao for apenas consulta, avaliar abrir o banco em modo somente leitura.
- Diretorios de log devem ser gravaveis pelo processo somente quando necessario.

Exemplo de permissao:

```bash
chown -R phonebook:phonebook /opt/phone_book
chmod 750 /opt/phone_book
chmod 640 /opt/phone_book/generate_names/db/bigon_bookX.db
```

Se o SQLite precisar escrever arquivos auxiliares, como WAL ou journal, o diretorio do banco tambem precisa ter permissao de escrita controlada para o usuario da aplicacao.

### 6.3 Criterios de Aceite

- A aplicacao nao roda como `root`.
- O banco nao fica gravavel por usuarios nao autorizados.
- Arquivos sensiveis nao possuem permissao `777`.
- A equipe documentou se o banco roda em modo leitura ou leitura/escrita.

## 7. Backup Criptografado

### 7.1 Requisito

O banco de dados deve ter backup automatico, criptografado e testado periodicamente.

### 7.2 Implementacao Recomendada

O processo de backup deve:

- Executar em agenda definida, por exemplo diaria.
- Gerar copia consistente do SQLite.
- Criptografar o arquivo antes de enviar para armazenamento externo.
- Manter politica de retencao.
- Testar restauracao em ambiente separado.

Para SQLite, usar copia segura. Quando possivel, usar mecanismo de backup do SQLite ou parar a aplicacao durante a copia se o banco estiver em escrita.

Exemplo conceitual:

```bash
sqlite3 bigon_bookX.db ".backup '/tmp/bigon_bookX.backup.db'"
gpg --symmetric --cipher-algo AES256 /tmp/bigon_bookX.backup.db
```

Armazenamento recomendado:

- Bucket privado com versionamento.
- Cofre corporativo de backups.
- Storage com criptografia em repouso.

### 7.3 Criterios de Aceite

- Existe rotina automatizada de backup.
- Backup e criptografado antes de sair do servidor.
- Chaves de criptografia nao ficam no mesmo diretorio do backup.
- Restauracao foi testada e documentada.
- Existe politica de retencao, por exemplo 7 diarios, 4 semanais e 12 mensais.

## 8. Logs Centralizados e Monitoramento

### 8.1 Requisito

Logs da aplicacao e do proxy devem ser coletados centralmente para auditoria, investigacao e alertas.

### 8.2 Eventos Minimos para Log

O sistema deve registrar:

- Rate limit acionado.
- Query invalida.
- Query longa demais.
- Erros internos.
- Timeouts de busca.
- Reinicios da aplicacao.
- Falhas de acesso ao banco.
- Status 4xx e 5xx no proxy.

Os logs nao devem registrar dados sensiveis em excesso. Quando necessario, mascarar ou resumir valores.

### 8.3 Solucoes Aceitas

Opcoes comuns:

- journald + forwarding.
- ELK/OpenSearch.
- Grafana Loki.
- Datadog.
- CloudWatch.
- Stack corporativa ja existente.

### 8.4 Alertas Recomendados

Criar alertas para:

- Aumento de respostas `500`.
- Aumento de `429 Too Many Requests`.
- Taxa incomum de `400 Bad Request`.
- Processo fora do ar.
- Uso elevado de CPU ou memoria.
- Disco com pouco espaco.
- Falha em backup.

### 8.5 Criterios de Aceite

- Logs da aplicacao e do proxy chegam ao coletor central.
- Equipe consegue buscar eventos por horario, IP, status HTTP e endpoint.
- Alertas criticos chegam ao canal operacional definido.
- Logs nao contem stack trace exposto ao usuario final.

## 9. Autenticacao Antes da Busca

### 9.1 Requisito

Se os dados consultados forem sensiveis, a busca nao deve ficar publica. O usuario deve autenticar antes de acessar `/api/search`.

### 9.2 Opcoes de Implementacao

Opcoes aceitas:

- Autenticacao via proxy com OAuth2/OIDC.
- Login corporativo com provedor como Google Workspace, Microsoft Entra ID ou outro IdP.
- Sessao no backend Go com cookies seguros.
- Token de API apenas para integracoes internas.

Para usuarios humanos, preferir OIDC/OAuth2 com cookies seguros.

### 9.3 Requisitos de Sessao

Se houver sessao por cookie:

- Cookie com `HttpOnly`.
- Cookie com `Secure`.
- Cookie com `SameSite=Lax` ou `Strict`.
- Expiracao definida.
- Logout funcional.
- Protecao contra CSRF quando houver operacoes mutaveis.

### 9.4 Controle de Acesso

Definir quem pode buscar:

- Todos os usuarios autenticados.
- Apenas grupo especifico.
- Perfis com permissao de consulta.

O controle deve ser validado no servidor, nao apenas no frontend.

### 9.5 Criterios de Aceite

- Usuario nao autenticado nao acessa `/api/search`.
- Usuario sem permissao recebe `403 Forbidden`.
- Sessao expirada exige novo login.
- O frontend nao armazena segredo sensivel.
- Tentativas negadas aparecem nos logs.

## 10. Execucao como Servico

### 10.1 Requisito

A aplicacao deve rodar como servico gerenciado pelo sistema operacional ou orquestrador.

### 10.2 Implementacao Recomendada com systemd

Exemplo conceitual:

```ini
[Unit]
Description=Phone Book API
After=network.target

[Service]
User=phonebook
Group=phonebook
WorkingDirectory=/opt/phone_book
ExecStart=/opt/phone_book/phone_book -server -port 8080 -db /opt/phone_book/generate_names/db/bigon_bookX.db
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/phone_book/generate_names/db

[Install]
WantedBy=multi-user.target
```

### 10.3 Criterios de Aceite

- Servico reinicia em falha.
- Servico nao roda como `root`.
- Logs sao capturados pelo sistema.
- Permissoes restritivas nao impedem acesso legitimo ao banco.

## 11. Checklist de Liberacao

Antes do deploy em producao, validar:

- HTTPS ativo e certificado valido.
- HTTP redirecionando para HTTPS.
- Porta `8080` inacessivel externamente.
- Aplicacao rodando com usuario dedicado.
- Banco com permissao minima.
- Backup criptografado configurado.
- Restauracao testada.
- Logs centralizados recebendo eventos.
- Alertas configurados.
- Rate limit validado.
- Timeout validado.
- Entradas invalidas retornando `400`.
- Erros internos retornando mensagem generica.
- Autenticacao ativa se os dados forem sensiveis.
- Testes automatizados passando.

## 12. Testes de Seguranca Recomendados

Executar os seguintes testes antes da liberacao:

- Consulta valida por nome.
- Consulta valida por ID.
- Consulta inexistente.
- Consulta com caracteres invalidos, por exemplo `99$`.
- Consulta longa acima do limite permitido.
- Muitas requisicoes em curto intervalo para validar `429`.
- Tentativa de acessar `/api/search` sem autenticacao, se autenticacao estiver ativa.
- Scan externo de portas.
- Validacao TLS com ferramenta corporativa ou SSL Labs.
- Restauracao de backup em ambiente separado.

## 13. Riscos Residuais

Mesmo com os controles acima, ainda existem riscos residuais:

- Ataques distribuidos podem contornar rate limit por IP.
- SQLite pode se tornar limitante se houver alto volume simultaneo.
- Cache em memoria e perdido ao reiniciar a aplicacao.
- Logs podem crescer rapidamente sem politica de retencao.
- Backups mal protegidos podem expor dados.

Mitigacoes futuras:

- WAF ou protecao anti-DDoS.
- Rate limit no proxy alem do backend.
- Migracao para banco cliente-servidor se a concorrencia crescer.
- Observabilidade com metricas e traces.
- Revisao periodica de permissoes e dependencias.

## 14. Entregaveis Esperados da Proxima Equipe

A equipe responsavel pela fase de producao deve entregar:

- Configuracao final do proxy reverso.
- Evidencia de HTTPS funcionando.
- Regras de firewall aplicadas.
- Usuario de sistema e permissoes documentadas.
- Rotina de backup criptografado.
- Evidencia de restauracao de backup.
- Configuracao de logs centralizados.
- Alertas operacionais configurados.
- Decisao formal sobre autenticacao.
- Evidencia de testes de seguranca executados.

## 15. Definicao de Pronto

A fase sera considerada pronta quando:

- A aplicacao estiver acessivel somente por HTTPS.
- O backend nao estiver exposto diretamente para a internet.
- O banco estiver protegido por permissoes minimas.
- Backups criptografados estiverem funcionando e restauracao tiver sido testada.
- Logs e alertas estiverem ativos.
- A autenticacao estiver implementada ou formalmente dispensada por decisao de risco.
- Todos os criterios de aceite desta especificacao estiverem atendidos.
