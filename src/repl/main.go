package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/trufae/mai/src/repl/llm"
)

// runStdinMode handles sending messages to LLM in stdin mode.
func runStdinMode(config *llm.Config, args []string) {
	input := readInput(args)

	// Create LLM client
	client, err := llm.NewLLMClient(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing LLM client: %v\n", err)
		os.Exit(1)
	}

	// Prepare messages from input
	messages := llm.PrepareMessages(input, config)

	// Prepare image if specified
	var images []string
	if config.ImagePath != "" {
		imageData, err := os.ReadFile(config.ImagePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading image file: %v\n", err)
			os.Exit(1)
		}

		encoded := base64.StdEncoding.EncodeToString(imageData)
		mimeType := http.DetectContentType(imageData)
		dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)
		images = append(images, dataURI)
		fmt.Fprintf(os.Stderr, "Attaching image: %s (%d bytes)\n", config.ImagePath, len(imageData))
	}

	// Send to LLM without streaming (for stdin mode)
	res, err := client.SendMessage(messages, false, images)
	if err != nil {
		fmt.Fprintf(os.Stderr, "REPL error: %v\n", err)
	}
	fmt.Println(res)
}

func loadConfig() *llm.Config {
	config := &llm.Config{
		OpenAPIHost:   getEnvOrDefault("OPENAPI_HOST", "localhost"),
		OpenAPIPort:   getEnvOrDefault("OPENAPI_PORT", "8989"),
		OllamaHost:    getEnvOrDefault("OLLAMA_HOST", "localhost"),
		OllamaPort:    getEnvOrDefault("OLLAMA_PORT", "11434"),
		BedrockRegion: getEnvOrDefault("AWS_REGION", "us-west-2"),
		PROVIDER:      getEnvOrDefault("MAI_PROVIDER", "ollama"),
		BaseURL:       getEnvOrDefault("MAI_BASEURL", ""),
		UserAgent:     getEnvOrDefault("MAI_USERAGENT", "mai-repl/1.0"),
		NoStream:      false,
	}

	// Backwards compatibility: if MAI_PROVIDER is not set, honor legacy API env var
	if os.Getenv("MAI_PROVIDER") == "" {
		if apiVal := os.Getenv("API"); apiVal != "" {
			config.PROVIDER = apiVal
		}
	}

	return config
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func readInput(args []string) string {
	var input strings.Builder

	// Add command line arguments
	if len(args) > 0 {
		input.WriteString(strings.Join(args, " "))
		input.WriteString("\n")
	}

	// Read from stdin
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		input.WriteString(scanner.Text())
		input.WriteString("\n")
	}

	// input.WriteString("</INPUT>")

	return input.String()
}

// makeRequest was unused; removed to simplify main.go.

func showEnvHelp() {
	fmt.Print(`
MAI_PROVIDER=[ollama | gemini | deepseek | claude | openai | mistral | bedrock]
MAI_BASEURL=[custom API base URL (e.g., https://api.moonshot.ai/anthropic)]
MAI_USERAGENT=[custom user agent string for HTTP requests]

Local:

OLLAMA_HOST=localhost
OLLAMA_PORT=11434
OLLAMA_MODEL=gemma3:1b
OLLAMA_MODEL=mannix/jan-nano:latest

Other:

GEMINI_API_KEY=(or set in ~/.r2ai.gemini-key)
OPENAI_API_KEY=(or set in ~/.r2ai.openai-key)
CLAUDE_API_KEY=(or set in ~/.r2ai.anthropic-key)
DEEPSEEK_API_KEY=(or set in ~/.r2ai.deepseek-key)
MISTRAL_API_KEY=(or set in ~/.r2ai.mistral-key)

Model Selection:

OPENAI_MODEL=o4-mini
CLAUDE_MODEL=claude-3-5-sonnet-20241022
MISTRAL_MODEL=mistral-large-latest

Bedrock-Specific:

AWS_ACCESS_KEY_ID=(or set in ~/.r2ai.bedrock-key)
AWS_REGION=us-west-2
BEDROCK_MODEL=anthropic.claude-3-5-sonnet-v1
`)
}
func showHelp() {
	fmt.Print(`$ mai-repl [--] | [-h] | [prompt] < INPUT
--               stdin mode (see -r)
-1               don't stream response, print once at the end
-a <string>      set the user agent for HTTP requests
-b <url>         specify a custom base URL for API requests
-c <key=value>   set configuration option
-h               show this help message
-H               show environment variables help (same as -hh)
-i <path>        attach an image to send to the model
-m <model>       select the model for the given provider
-n               do not load rc file and disable REPL history
-p <provider>    select the provider to use
-q               quit after running given actions
-s <string>      send string directly to AI (can be used multiple times)
-t               enable tools processing
-U               update project by running git pull ; make in project directory

Files:
.mai/rc (project or ~/.mai/rc)         : script to be loaded before the repl is shown
.mai/history.json (project or ~/.mai) : REPL command history file (JSON array)
.mai/chat (project or ~/.mai)         : storage for chat session files
.mai/systemprompt.md (project or ~/.mai) : system prompt file (supports '@' include directives)
./prompts          : directory containing custom prompts
`)
}

// setModel sets the generic model; providers handle defaults when empty
func setModel(config *llm.Config, model string) { config.Model = model }

// applyConfigOptionsToLLMConfig maps relevant ConfigOptions into the llm.Config
// so that stdin mode and providers see the same effective configuration.
func applyConfigOptionsToLLMConfig(config *llm.Config, opts *ConfigOptions) {
	if opts == nil {
		return
	}
	if v := opts.Get("ai.provider"); v != "" {
		config.PROVIDER = v
	}
	if v := opts.Get("ai.model"); v != "" {
		config.Model = v
	}
	if v := opts.Get("ai.baseurl"); v != "" {
		config.BaseURL = v
	}
	if v := opts.Get("http.useragent"); v != "" {
		config.UserAgent = v
	}

	// System prompt options
	if v := opts.Get("llm.systemprompt"); v != "" {
		config.SystemPrompt = v
	}
	if v := opts.Get("llm.systempromptfile"); v != "" {
		// Expand ~ if present
		if strings.HasPrefix(v, "~") {
			if home, err := os.UserHomeDir(); err == nil {
				v = filepath.Join(home, v[1:])
			}
		}
		config.SystemPromptFile = v
	}

	if v := opts.Get("dir.promptfile"); v != "" {
		if strings.HasPrefix(v, "~") {
			if home, err := os.UserHomeDir(); err == nil {
				v = filepath.Join(home, v[1:])
			}
		}
		config.PromptFile = v
	}
	// Behavior toggles used by providers
	if opts.Get("scr.markdown") != "" {
		config.Markdown = opts.GetBool("scr.markdown")
	}
	if opts.Get("ai.deterministic") != "" {
		config.Deterministic = opts.GetBool("ai.deterministic")
	}
	if opts.Get("llm.rawmode") != "" {
		config.Rawdog = opts.GetBool("llm.rawmode")
	}

	// Debug flag: when enabled, show raw messages sent to providers
	if opts.Get("repl.debug") != "" {
		config.Debug = opts.GetBool("repl.debug")
	}

	// Conversation message limit: number of recent messages to include when sending
	if v := opts.Get("chat.tail"); v != "" {
		if num, err := opts.GetNumber("chat.tail"); err == nil {
			config.ConversationMessageLimit = int(num)
		}
	}

	// Auto-compact option is handled at REPL level; mirror into options for visibility
	if v := opts.Get("chat.autocompact"); v != "" {
		// no direct mapping into llm.Config required; REPL reads configOptions
	}
	// Structured output schema: prefer schemafile if provided, else inline schema
	if path := opts.Get("llm.schemafile"); path != "" {
		if strings.HasPrefix(path, "~") {
			if home, err := os.UserHomeDir(); err == nil {
				path = filepath.Join(home, path[1:])
			}
		}
		if content, err := os.ReadFile(path); err == nil {
			var schema map[string]interface{}
			if err := json.Unmarshal(content, &schema); err == nil {
				config.Schema = schema
			}
		}
	} else if inline := opts.Get("llm.schema"); inline != "" {
		var schema map[string]interface{}
		if err := json.Unmarshal([]byte(inline), &schema); err == nil {
			config.Schema = schema
		}
	}
}

func main() {
	args := os.Args[1:]

	if len(args) > 0 && args[0] == "-h" {
		showHelp()
		return
	}
	if len(args) > 0 && (args[0] == "-hh" || args[0] == "-H") {
		showEnvHelp()
		return
	}

	config := loadConfig()

	// Slice to store script strings from -s flags
	var scriptStrings []string

	// Flag to quit after running actions
	quitAfterActions := false

	// For backwards compatibility - check if API env var is set
	if apiVal := os.Getenv("API"); apiVal != "" && os.Getenv("MAI_PROVIDER") == "" {
		config.PROVIDER = apiVal
	}
	// MAI_MODEL is deprecated; use -m or /set model instead

	configOptions := NewConfigOptions()
	// Process command line flags
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-n":
			// Skip loading rc file and disable REPL history
			configOptions.Set("repl.history", "false")
			configOptions.Set("repl.skiprc", "true")
			config.SkipRcFile = true
			args = append(args[:i], args[i+1:]...)
			i--
		case "-t":
			// Enable legacy tools flow
			configOptions.Set("tools.old", "true")
			args = append(args[:i], args[i+1:]...)
			i--
		case "-1":
			config.NoStream = true
			// Keep REPL in sync with stdin mode: disable streaming in options
			configOptions.Set("llm.stream", "false")
			args = append(args[:i], args[i+1:]...)
			i--
		case "-i":
			if i+1 < len(args) {
				config.ImagePath = args[i+1]
				args = append(args[:i], args[i+2:]...)
				i--
			} else {
				fmt.Fprintf(os.Stderr, "Error: -i requires an image path argument\n")
				os.Exit(1)
			}
		case "-p":
			if i+1 < len(args) {
				config.PROVIDER = args[i+1]
				// Keep REPL options in sync so /get reflects this
				configOptions.Set("ai.provider", args[i+1])
				args = append(args[:i], args[i+2:]...)
				i--
			} else {
				fmt.Fprintf(os.Stderr, "Error: -p requires a provider argument\n")
				os.Exit(1)
			}
		case "-b":
			if i+1 < len(args) {
				config.BaseURL = args[i+1]
				// Mirror into options for REPL visibility
				configOptions.Set("ai.baseurl", args[i+1])
				args = append(args[:i], args[i+2:]...)
				i--
			} else {
				fmt.Fprintf(os.Stderr, "Error: -b requires a base URL argument\n")
				os.Exit(1)
			}
		case "-m":
			if i+1 < len(args) {
				setModel(config, args[i+1])
				// Also set generic model option for REPL
				configOptions.Set("ai.model", args[i+1])
				args = append(args[:i], args[i+2:]...)
				i--
			} else {
				fmt.Fprintf(os.Stderr, "Error: -m requires a model argument\n")
				os.Exit(1)
			}
		case "-a":
			if i+1 < len(args) {
				config.UserAgent = args[i+1]
				// Mirror into options so /get useragent shows it
				configOptions.Set("http.useragent", args[i+1])
				args = append(args[:i], args[i+2:]...)
				i--
			} else {
				fmt.Fprintf(os.Stderr, "Error: -a requires a user agent string\n")
				os.Exit(1)
			}
		case "-c":
			if i+1 < len(args) {
				configArg := args[i+1]
				// Parse config option in format key=value
				parts := strings.SplitN(configArg, "=", 2)
				if len(parts) != 2 {
					fmt.Fprintf(os.Stderr, "Error: -c requires format 'key=value'\n")
					os.Exit(1)
				}
				// Set the option in config
				key, value := parts[0], parts[1]
				configOptions.Set(key, value)
				args = append(args[:i], args[i+2:]...)
				i--
			} else {
				fmt.Fprintf(os.Stderr, "Error: -c requires a config key=value pair\n")
				os.Exit(1)
			}
		case "-q":
			quitAfterActions = true
			args = append(args[:i], args[i+1:]...)
			i--
		case "-s":
			if i+1 < len(args) {
				scriptStrings = append(scriptStrings, args[i+1])
				args = append(args[:i], args[i+2:]...)
				i--
			} else {
				fmt.Fprintf(os.Stderr, "Error: -s requires a string argument\n")
				os.Exit(1)
			}
		case "-U":
			// Update project by running git pull ; make in project directory
			projectDir, err := resolveProjectDirectory()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error resolving project directory: %v\n", err)
				os.Exit(1)
			}
			if err := updateProject(projectDir); err != nil {
				fmt.Fprintf(os.Stderr, "Error updating project: %v\n", err)
				os.Exit(1)
			}
			return
		}
	}

	// Apply -c options into the llm.Config so both REPL and stdin modes see them
	applyConfigOptionsToLLMConfig(config, configOptions)

	// Send strings from -s flags to AI if any
	if len(scriptStrings) > 0 {
		// Apply config options to LLM config
		applyConfigOptionsToLLMConfig(config, configOptions)

		// Create LLM client
		client, err := llm.NewLLMClient(config)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error initializing LLM client: %v\n", err)
			os.Exit(1)
		}

		for _, scriptString := range scriptStrings {
			fmt.Printf("Sending to AI: %s\n", scriptString)

			// Prepare messages from the string
			messages := llm.PrepareMessages(scriptString, config)

			// Send to LLM without streaming
			res, err := client.SendMessage(messages, false, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error sending to AI: %v\n", err)
			} else {
				fmt.Println(res)
			}
		}
		if quitAfterActions {
			return
		}
	}

	// Check for REPL mode: interactive terminal or explicit -r flag
	stdinIsTerminal := term.IsTerminal(int(os.Stdin.Fd()))
	if stdinIsTerminal {
		// Not stdin mode, will load 'rc' file and start REPL
		config.IsStdinMode = false

		repl, err := NewREPL(*configOptions)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error initializing REPL: %v\n", err)
			os.Exit(1)
		}

		// TODO: use MAI_COLORS ?
		if err := repl.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "REPL error: %v\n", err)
			os.Exit(1)
		}
	} else {
		config.IsStdinMode = true
		runStdinMode(config, args)
	}
}

// resolveProjectDirectory resolves the project directory by following the symlink of argv0
// similar to how it's done for doc/ and prompts/ directories
func resolveProjectDirectory() (string, error) {
	// Get the executable path (argv0)
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not determine executable path: %v", err)
	}

	// Follow symlink if the executable is a symlink
	realPath, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		// Fall back to the original path if symlink evaluation fails
		realPath = execPath
	}

	// Get the directory containing the executable
	execDir := filepath.Dir(realPath)

	// Start searching from the executable directory and go up to find a git repository
	currentDir := execDir
	for {
		// Check if this directory contains a .git folder (indicating a git repository)
		gitDir := filepath.Join(currentDir, ".git")
		if _, err := os.Stat(gitDir); err == nil {
			// Found a git repository, return this directory
			return currentDir, nil
		}

		// Move up one directory
		parentDir := filepath.Dir(currentDir)

		// Stop if we've reached the root directory
		if parentDir == currentDir {
			break
		}

		// Continue with the parent directory
		currentDir = parentDir
	}

	return "", fmt.Errorf("no git repository found in executable path hierarchy")
}

// updateProject runs "git pull ; make" in the specified project directory
func updateProject(projectDir string) error {
	// Change to the project directory
	if err := os.Chdir(projectDir); err != nil {
		return fmt.Errorf("failed to change to project directory %s: %v", projectDir, err)
	}

	fmt.Printf("Updating project in %s...\n", projectDir)

	// Run git pull
	fmt.Println("Running git pull...")
	gitCmd := exec.Command("git", "pull")
	gitCmd.Stdout = os.Stdout
	gitCmd.Stderr = os.Stderr
	if err := gitCmd.Run(); err != nil {
		return fmt.Errorf("git pull failed: %v", err)
	}

	// Run make
	fmt.Println("Running make...")
	makeCmd := exec.Command("make")
	makeCmd.Stdout = os.Stdout
	makeCmd.Stderr = os.Stderr
	if err := makeCmd.Run(); err != nil {
		return fmt.Errorf("make failed: %v", err)
	}

	fmt.Println("Project updated successfully!")
	return nil
}
