package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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
	messages := llm.PrepareMessages(input)

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
		OpenAPIPort:   getEnvOrDefault("OPENAPI_PORT", "8080"),
		OllamaHost:    getEnvOrDefault("OLLAMA_HOST", "localhost"),
		OllamaPort:    getEnvOrDefault("OLLAMA_PORT", "11434"),
		BedrockRegion: getEnvOrDefault("AWS_REGION", "us-west-2"),
		PROVIDER:      getEnvOrDefault("MAI_PROVIDER", "ollama"),
		GeminiKey:     os.Getenv("GEMINI_API_KEY"),
		OpenAIKey:     os.Getenv("OPENAI_API_KEY"),
		ClaudeKey:     os.Getenv("CLAUDE_API_KEY"),
		DeepSeekKey:   os.Getenv("DEEPSEEK_API_KEY"),
		MistralKey:    os.Getenv("MISTRAL_API_KEY"),
		BedrockKey:    os.Getenv("AWS_ACCESS_KEY_ID"),
		XAIKey:        os.Getenv("XAI_API_KEY"),
		BaseURL:       getEnvOrDefault("MAI_BASEURL", ""),
		UserAgent:     getEnvOrDefault("MAI_USERAGENT", "mai-repl/1.0"),
		NoStream:      false,
		// options:       &llm.Config{}, // NewConfigOptions(), // Initialize configuration options
		// configOptions:       NewConfigOptions(), // Initialize configuration options
	}

	// Backwards compatibility: if MAI_PROVIDER is not set, honor legacy API env var
	if os.Getenv("MAI_PROVIDER") == "" {
		if apiVal := os.Getenv("API"); apiVal != "" {
			config.PROVIDER = apiVal
		}
	}

	// Load API keys from files if environment variables are not set
	if config.GeminiKey == "" {
		if key := readKeyFile("~/.r2ai.gemini-key"); key != "" {
			config.GeminiKey = key
		}
	}
	if config.OpenAIKey == "" {
		if key := readKeyFile("~/.r2ai.openai-key"); key != "" {
			config.OpenAIKey = key
		}
	}
	if config.ClaudeKey == "" {
		if key := readKeyFile("~/.r2ai.anthropic-key"); key != "" {
			config.ClaudeKey = key
		}
	}
	if config.DeepSeekKey == "" {
		if key := readKeyFile("~/.r2ai.deepseek-key"); key != "" {
			config.DeepSeekKey = key
		}
	}
	if config.MistralKey == "" {
		if key := readKeyFile("~/.r2ai.mistral-key"); key != "" {
			config.MistralKey = key
		}
	}
	if config.BedrockKey == "" {
		if key := readKeyFile("~/.r2ai.bedrock-key"); key != "" {
			config.BedrockKey = key
		}
	}
	if config.XAIKey == "" {
		if key := readKeyFile("~/.r2ai.xai-key"); key != "" {
			config.XAIKey = key
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

func readKeyFile(path string) string {
	// Expand ~ to home directory
	if strings.HasPrefix(path, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		path = filepath.Join(homeDir, path[1:])
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func readInput(args []string) string {
	var input strings.Builder

	// Add command line arguments
	if len(args) > 0 {
		input.WriteString(strings.Join(args, " "))
		input.WriteString("\n")
	}

	// input.WriteString("<INPUT>\n")

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
	if v := opts.Get("provider"); v != "" {
		config.PROVIDER = v
	}
	if v := opts.Get("model"); v != "" {
		config.Model = v
	}
	if v := opts.Get("baseurl"); v != "" {
		config.BaseURL = v
	}
	if v := opts.Get("useragent"); v != "" {
		config.UserAgent = v
	}
	// Behavior toggles used by providers
	if opts.Get("markdown") != "" {
		config.Markdown = opts.GetBool("markdown")
	}
	if opts.Get("deterministic") != "" {
		config.Deterministic = opts.GetBool("deterministic")
	}
	if opts.Get("rawdog") != "" {
		config.Rawdog = opts.GetBool("rawdog")
	}
	// Structured output schema: prefer schemafile if provided, else inline schema
	if path := opts.Get("schemafile"); path != "" {
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
	} else if inline := opts.Get("schema"); inline != "" {
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
			configOptions.Set("history", "false")
			configOptions.Set("skiprc", "true")
			config.SkipRcFile = true
			args = append(args[:i], args[i+1:]...)
			i--
		case "-t":
			// Set usetools to true
			configOptions.Set("usetools", "true")
			args = append(args[:i], args[i+1:]...)
			i--
		case "-1":
			config.NoStream = true
			// Keep REPL in sync with stdin mode: disable streaming in options
			configOptions.Set("stream", "false")
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
				configOptions.Set("provider", args[i+1])
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
				configOptions.Set("baseurl", args[i+1])
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
				configOptions.Set("model", args[i+1])
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
				configOptions.Set("useragent", args[i+1])
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
			messages := llm.PrepareMessages(scriptString)

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
		repl.configOptions.Set("markdown", "false")
		if err := repl.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "REPL error: %v\n", err)
			os.Exit(1)
		}
	} else {
		config.IsStdinMode = true
		runStdinMode(config, args)
	}
}
