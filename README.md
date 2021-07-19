# projekt-gutenberg-de-dl

Lädt öffentlich zugängliche Literatur von projekt-gutenberg.org herunter und speichert diese als Markdown-Dokument ab.

Downloads free public domain German literature from projekt-gutenberg.org and converts it into Markdown.

## Aufbauen des Programms vom Quellcode / Building from source

- `git clone 'https://git.nobrain.org/r4/projekt-gutenberg-de-dl.git'`

- `cd projekt-gutenberg-de-dl`

- `go mod tidy`

- `go build`

## Nutzung / Usage

- `./projekt_gutenberg_de_dl`

### Beispiel / Example

- `mkdir out`

- `./projekt_gutenberg_de_dl -dir 'out' 'https://www.projekt-gutenberg.org/nietzsch/zara/zara.html'`