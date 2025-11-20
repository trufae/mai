# mai.vim

A Vim plugin that integrates the `mai` AI assistant into your Vim workflow. Send selected text to `mai` with predefined prompts and apply the AI-generated output back to your buffer.

## Requirements

- The `mai` command-line tool installed and available in your PATH
- By default, uses Ollama with Gemma3:1B model

## Installation

```bash
make install
```

## Configuration

Your ~/.vimrc will look like this:

```vim
source ~/.vim/mai/mai.vim
let g:mai_provider = 'ollama'
let g:mai_model = 'gemma3:12b'
let g:mai_color = 0
let g:mai_defaction = 3
xnoremap m :<C-U>call Mai()<CR>
```

### AI Provider and Model

Set your preferred AI provider and model in your `~/.vimrc`:

```vim
let g:mai_provider = 'openai'  " or 'anthropic', 'ollama', etc.
let g:mai_model = 'gpt-4o'     " model name depends on provider
```

### Custom Prompts

Edit `~/.vim/mai/prompts.txt` to add your own prompts. Each line is a separate prompt.

## Usage

1. 🔍 Select text in visual mode (or use the current line in normal mode)
2. ⌨️ Press `m` to invoke mai.vim
3. 📋 Choose a prompt from the menu
4. 👀 Review the AI output
5. 🔄 Select how to apply the output:
   - ❌ Ignore
   - 🔄 Replace selected text
   - ➕ Append below
   - 🛠️ Wrap in C preprocessor conditional block
   - 📄 Show in a separate split

## Key Mappings

- ⌨️ Visual mode: `m` - Process selected text with mai

## Examples

- 🔧 Fix typos: Select text, press `m`, choose "fix typos", replace selection
- 🌐 Translate: Select text, press `m`, choose "translate to catalan", append below
- ✍️ Improve writing: Select text, press `m`, choose "improve wording", replace selection

## Uninstallation

```bash
make uninstall
```

This removes the plugin files and the source line from `~/.vimrc`.
