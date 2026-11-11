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

`PORT` e `NOTES_DIR` também podem fornecer os valores padrão. O banco fica em `notes.db` dentro do diretório de dados. Na primeira inicialização, o app cria o índice FTS5 e o índice de vínculos entre notas, indexando as notas existentes sem substituir os registros.

Para exigir senha, defina `AUTHENTICATION_PASSWORD` antes de iniciar:

```sh
AUTHENTICATION_PASSWORD='uma-senha-forte' ./run.sh
```

O login protege a interface principal e os endpoints da API, exceto `GET /api/health` e as rotas de login/status/logout. A sessão usa cookie HttpOnly/SameSite Strict, expira em 12 horas e é invalidada quando o servidor reinicia. Após cinco senhas incorretas do mesmo endereço IP, novas tentativas são limitadas por 15 minutos. Sem `AUTHENTICATION_PASSWORD`, o app permanece sem autenticação.

Use uma senha forte e HTTPS quando o app estiver acessível pela rede. Se HTTPS terminar em um proxy reverso, force o atributo Secure do cookie:

```sh
AUTHENTICATION_PASSWORD='uma-senha-forte' AUTHENTICATION_COOKIE_SECURE=true ./run.sh
```

`AUTHENTICATION_COOKIE_SECURE=true` deve ser usado somente quando o navegador acessa o app por HTTPS; caso contrário, o navegador não enviará o cookie.

Antes de iniciar, o script verifica a porta escolhida e encerra uma instância anterior do PKM Notes que esteja escutando nela. Processos de outros aplicativos não são encerrados; nesse caso, escolha outra porta ou encerre o processo manualmente. A verificação requer `lsof` ou `fuser`.

Para ARM64, compile nativamente no dispositivo ou configure um compilador C cruzado para o alvo. Como SQLite usa `cgo`, `GOARCH=arm64` isoladamente não produz um binário executável com o driver.

## Recursos do MVP

- Busca paginada por título ou conteúdo, com FTS5.
- Árvore de pastas virtual com notas clicáveis dentro de cada pasta; os itens são carregados sob demanda em lotes e podem ser movidos por arrastar e soltar.
- Abrir notas pela árvore ou pelos cards de busca no painel principal, sem janela modal.
- Criar, editar e excluir notas com salvamento automático no SQLite e restauração da pesquisa ao voltar; notas e pastas podem ser reorganizadas pelo explorador lateral.
- Anexos enviados pelo editor ficam em `attachments/<id-da-nota>/` no diretório de dados, sem depender do caminho virtual da nota. O editor oferece upload, download e exclusão, insere links Markdown no conteúdo e pode mostrar imagens raster compatíveis no preview. O limite é 25 MiB por arquivo.
- A API calcula backlinks e relações a partir de links Markdown e wikilinks resolvidos pelo título/caminho virtual das notas. A interface inclui um grafo interativo com filtro por título/pasta, destaque de conexões, zoom, movimento e abertura de notas.
- Editor Markdown próprio, inspirado no fluxo do Basalt, com barra de formatação, atalhos (`Ctrl/Cmd+B`, `I`, `K` e `Ctrl/Cmd+Alt+T` para inserir tabela; `Ctrl/Cmd+Alt+1/2/3` para escolher o modo e `Ctrl/Cmd+Alt+V` para alternar), desfazer/refazer, divisor redimensionável e modos dividido, edição e leitura.
- Tags são lidas do frontmatter Markdown (`tags: sei-la`, lista YAML ou lista inline); tags legadas são migradas para o frontmatter quando a nota é aberta para edição.
- Interface adaptada para desktop e mobile; tema claro/escuro.

O preview usa Goldmark no backend. HTML bruto é mantido inativo e destinos com esquemas inseguros em links/imagens são neutralizados.

## Dados, anexos e backup

- As notas, seus caminhos de pasta virtuais e metadados ficam em `notes.db` no diretório de dados. O índice de busca FTS5 e o índice de vínculos (`note_links`) são mantidos no mesmo banco. O grafo e os backlinks são resolvidos a partir desse índice, sem reler o conteúdo de todas as notas a cada consulta.
- Anexos são gravados em `attachments/<id-da-nota>/` no mesmo diretório de dados. O nome físico é aleatório; o nome original e o tipo de mídia ficam nos metadados SQLite. Assim, mover a nota para outra pasta virtual não move nem quebra o arquivo.
- Ao excluir uma nota, seus metadados de anexos são removidos pelo SQLite e os arquivos daquela nota são apagados do disco.
- Para fazer backup completo, pare o servidor e copie **o arquivo `notes.db` e a pasta `attachments/` juntos**. Restaurar somente o banco preserva as referências, mas não recupera o conteúdo dos arquivos; restaurar somente os anexos não recupera as notas nem os metadados.
- As pastas exibidas na árvore são virtuais: não são criadas como diretórios no disco. Apenas os anexos usam armazenamento em arquivos.

Ao anexar um arquivo pelo editor, uma imagem raster compatível é inserida como `![nome](URL)` e outros arquivos como `[nome](URL)`. A URL usa o ID da nota e do anexo; com autenticação ativada, o download também requer uma sessão válida.

## API principal

- `GET /api/notes?limit=30&offset=0&folder=...`
- `POST /api/notes`
- `GET`, `PUT` e `DELETE /api/notes/{id}`
- `GET /api/search?q=...&mode=title|content&limit=30&offset=0&folder=...`
- `GET /api/folders`
- `POST /api/folders/move` com `{ "source": "Antiga", "target": "Arquivo" }`
- `PATCH /api/notes/{id}/folder` com `{ "folder": "Projetos" }`
- `GET /api/folder-notes?folder=Projetos&limit=100&offset=0` (inclua `folder=` para listar somente notas sem pasta)
- `GET /api/notes/{id}/backlinks` e `GET /api/graph`
- `GET` e `POST /api/notes/{id}/attachments`; `GET` e `DELETE /api/notes/{id}/attachments/{attachment_id}` (upload multipart no campo `file`)
- `GET /api/auth/status`, `POST /api/auth/login` e `POST /api/auth/logout`
- `POST /api/preview` com o corpo Markdown em texto simples
- `GET /api/health`

Listagens aceitam limites de 1 a 100 registros. Títulos aceitam até 256 caracteres, conteúdo até 5 MiB e caminhos de pasta devem ser relativos. Uploads aceitam um arquivo de até 25 MiB; são enviados como `multipart/form-data` usando o campo `file`. Links para anexos são servidos como download; imagens raster podem ser exibidas inline com `?inline=1`.

O endpoint do grafo retorna `{ "nodes": [{ "id", "title", "folder" }], "edges": [{ "source", "target", "type" }] }`. Os tipos de aresta são `markdown` e `wikilink`. Wikilinks aceitam `[[Título]]` e `[[Título|alias]]`; links Markdown locais resolvem caminhos de nota com ou sem extensão `.md`. URLs externas não viram relações entre notas.

Com autenticação habilitada, `POST /api/auth/login` recebe `{ "password": "..." }` em JSON e define o cookie de sessão; o navegador deve manter esse cookie nas chamadas seguintes. `GET /api/auth/status` informa se a autenticação está habilitada e se a sessão atual está autenticada.

As pastas continuam virtuais: são valores no SQLite derivados das notas, não diretórios físicos. O editor é uma implementação própria; nenhum código-fonte do Basalt foi copiado.

## Testes

```sh
cd backend
go test -tags sqlite_fts5 ./...
go vet -tags sqlite_fts5 ./...
go test -tags sqlite_fts5 -run '^$' -bench BenchmarkNoteGraph ./...
cd ../web/frontend
npm test
```
