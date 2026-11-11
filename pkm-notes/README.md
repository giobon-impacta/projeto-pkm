# PKM Notes

Aplicação local de notas Markdown com backend Go, SQLite/FTS5 e interface responsiva. As pastas mostradas na navegação são caminhos virtuais armazenados no SQLite; o app não cria arquivos `.md` no disco.

## Requisitos

- Go 1.21 ou superior.
- Um compilador C disponível para o `cgo` (o driver SQLite compila o SQLite junto ao binário).

## Executar

Na raiz do projeto:

```sh
./run.sh
```

O script compila o backend com FTS5 e inicia o servidor em `http://localhost:8000`. É possível configurar:

```sh
./run.sh --data-dir ./meus-dados --port 8080
```

`PORT` e `NOTES_DIR` também podem fornecer os valores padrão. O banco fica em `notes.db` dentro do diretório de dados. Na primeira inicialização, o app cria o índice FTS5 e indexa as notas existentes sem substituir os registros.

Antes de iniciar, o script verifica a porta escolhida e encerra uma instância anterior do PKM Notes que esteja escutando nela. Processos de outros aplicativos não são encerrados; nesse caso, escolha outra porta ou encerre o processo manualmente. A verificação requer `lsof` ou `fuser`.

Para ARM64, compile nativamente no dispositivo ou configure um compilador C cruzado para o alvo. Como SQLite usa `cgo`, `GOARCH=arm64` isoladamente não produz um binário executável com o driver.

## Recursos do MVP

- Busca paginada por título ou conteúdo, com FTS5.
- Árvore de pastas virtual com notas clicáveis dentro de cada pasta; os itens são carregados sob demanda em lotes e podem ser movidos por arrastar e soltar.
- Abrir notas pela árvore ou pelos cards de busca no painel principal, sem janela modal.
- Criar, editar e excluir notas com salvamento automático no SQLite e restauração da pesquisa ao voltar; notas e pastas podem ser reorganizadas pelo explorador lateral.
- Editor Markdown próprio, inspirado no fluxo do Basalt, com barra de formatação, atalhos, desfazer/refazer, divisor redimensionável e modos dividido, edição e leitura.
- Tags são lidas do frontmatter Markdown (`tags: sei-la`, lista YAML ou lista inline); tags legadas são migradas para o frontmatter quando a nota é aberta para edição.
- Interface adaptada para desktop e mobile; tema claro/escuro.

O preview usa Goldmark no backend. HTML bruto é mantido inativo e destinos com esquemas inseguros em links/imagens são neutralizados.

## API principal

- `GET /api/notes?limit=30&offset=0&folder=...`
- `POST /api/notes`
- `GET`, `PUT` e `DELETE /api/notes/{id}`
- `GET /api/search?q=...&mode=title|content&limit=30&offset=0&folder=...`
- `GET /api/folders`
- `POST /api/folders/move` com `{ "source": "Antiga", "target": "Arquivo" }`
- `PATCH /api/notes/{id}/folder` com `{ "folder": "Projetos" }`
- `GET /api/folder-notes?folder=Projetos&limit=100&offset=0` (inclua `folder=` para listar somente notas sem pasta)
- `POST /api/preview` com o corpo Markdown em texto simples
- `GET /api/health`

Listagens aceitam limites de 1 a 100 registros. Títulos aceitam até 256 caracteres, conteúdo até 5 MiB e caminhos de pasta devem ser relativos. O editor é uma implementação própria; nenhum código-fonte do Basalt foi copiado.

## Testes

```sh
cd backend
go test -tags sqlite_fts5 ./...
go vet -tags sqlite_fts5 ./...
cd ../web/frontend
npm test
```
