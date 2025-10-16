# mai.vim

A Vim plugin that integrates the `mai` AI assistant into your Vim workflow. Send selected text to `mai` with predefined prompts and apply the AI-generated output back to your buffer.

## Requirements

- Vim with `+python` support (optional, for advanced features)
- The `mai` command-line tool installed and available in your PATH
- An API key configured for your chosen AI provider (see `mai --help`)

## Installation

### Automatic Installation

Run the provided Makefile:

```bash
make install
```

This will:
- Create the directory `~/.vim/mai/`
- Copy `mai.vim` and `prompts.txt` to `~/.vim/mai/`
- Add `source ~/.vim/mai/mai.vim` to your `~/.vimrc`

### Manual Installation

1. Copy `mai.vim` to `~/.vim/mai/mai.vim`
2. Copy `prompts.txt` to `~/.vim/mai/prompts.txt`
3. Add this line to your `~/.vimrc`:
   ```vim
   source ~/.vim/mai/mai.vim
   ```

## Configuration

### AI Provider and Model

Set your preferred AI provider and model in your `~/.vimrc`:

```vim
let g:mai_provider = 'openai'  " or 'anthropic', 'ollama', etc.
let g:mai_model = 'gpt-4o'     " model name depends on provider
```

### Custom Prompts

Edit `~/.vim/mai/prompts.txt` to add your own prompts. Each line is a separate prompt.

## Usage

1. Select text in visual mode (or use the current line in normal mode)
2. Press `m` to invoke mai.vim
3. Choose a prompt from the menu
4. Review the AI output
5. Select how to apply the output:
   - Ignore
   - Replace selected text
   - Append below
   - Wrap in C preprocessor conditional block
   - Show in a separate split

## Key Mappings

- Visual mode: `m` - Process selected text with mai

## Examples

- Fix typos: Select text, press `m`, choose "fix typos", replace selection
- Translate: Select text, press `m`, choose "translate to catalan", append below
- Improve writing: Select text, press `m`, choose "improve wording", replace selection

## Uninstallation

```bash
make uninstall
```

This removes the plugin files and the source line from `~/.vimrc`.