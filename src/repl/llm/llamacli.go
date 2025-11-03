package llm

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/trufae/mai/src/repl/art"
)

// LlamaCliProvider implements the LLM provider interface for llama-cli
type LlamaCliProvider struct {
	BaseProvider
}

func NewLlamaCliProvider(config *Config, ctx context.Context) *LlamaCliProvider {
	return &LlamaCliProvider{
		BaseProvider: BaseProvider{
			config: config,
			ctx:    ctx,
		},
	}
}

func (p *LlamaCliProvider) GetName() string {
	return "LlamaCli"
}

func (p *LlamaCliProvider) DefaultModel() string {
	if v := os.Getenv("LLAMACLI_MODEL"); v != "" {
		return v
	}
	return ""
}

func (p *LlamaCliProvider) IsAvailable() bool {
	_, err := exec.LookPath("llama-cli")
	return err == nil
}

func (p *LlamaCliProvider) ListModels(ctx context.Context) ([]Model, error) {
	// For llama-cli, models are local files, so we can't list them
	return []Model{}, nil
}

func (p *LlamaCliProvider) SendMessage(messages []Message, stream bool, images []string, tools []OpenAITool) (string, error) {
	if len(images) > 0 {
		return "", fmt.Errorf("images not supported by llama-cli provider")
	}
	if len(tools) > 0 {
		return "", fmt.Errorf("tools not supported by llama-cli provider")
	}

	effectiveModel := p.config.Model
	if effectiveModel == "" {
		effectiveModel = p.DefaultModel()
	}
	if effectiveModel == "" {
		return "", fmt.Errorf("no model specified for llama-cli provider")
	}

	// Build prompt from messages
	prompt := BuildConversationString(messages, p.config.ConversationIncludeLLM, p.config.ConversationIncludeSystem, p.config.ConversationFormat, p.config.ConversationUseLastUser)

	if p.config.Debug {
		art.DebugBanner("LlamaCli Prompt", prompt)
	}

	// Start llama-cli in simple-io mode
	cmd := exec.CommandContext(p.ctx, "llama-cli",
		"-m", effectiveModel,
		"--simple-io",
	)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}

	if err := cmd.Start(); err != nil {
		return "", err
	}

	// Drain stderr in background
	go func() {
		io.Copy(io.Discard, stderr)
	}()

	// Write prompt
	if _, err := io.WriteString(stdin, prompt); err != nil {
		return "", err
	}

	if stream {
		return p.streamResponse(stdout, stdin, cmd)
	} else {
		return p.nonStreamResponse(stdout, stdin, cmd)
	}
}

func (p *LlamaCliProvider) streamResponse(stdout io.ReadCloser, stdin io.WriteCloser, cmd *exec.Cmd) (string, error) {
	defer stdin.Close()
	defer cmd.Wait()

	scanner := bufio.NewScanner(stdout)
	var fullResponse strings.Builder
	sd := NewStreamDemo(nil, nil, nil)

	markdownEnabled := p.config.Markdown
	if markdownEnabled {
		ResetStreamRenderer()
	}

	printed := false
	for scanner.Scan() {
		select {
		case <-p.ctx.Done():
			return "", p.ctx.Err()
		default:
		}

		line := scanner.Text()
		if line == "" {
			continue
		}

		// In simple-io mode, each line is part of the response
		raw := line + "\n"

		sd.OnToken(raw)
		toPrint := raw
		if thinkHideEnabled || thinkDropLeading {
			toPrint = FilterOutThinkForOutput(toPrint)
		}
		if p.config.DemoMode && !printed {
			toPrint = strings.TrimLeft(toPrint, " \t\r\n")
		}
		formatted := FormatStreamingChunk(toPrint, markdownEnabled)
		fmt.Print(formatted)
		if toPrint != "" {
			printed = true
		}
		fullResponse.WriteString(raw)
	}

	if markdownEnabled {
		renderer := GetStreamRenderer()
		if final := renderer.Flush(); final != "" {
			EmitDemoTokens(final)
			if p.config.ThinkHide {
				trimmed := FilterOutThinkForOutput(final)
				if !printed {
					trimmed = strings.TrimLeft(trimmed, " \t\r\n")
				}
				fmt.Print(trimmed)
			} else {
				trimmed := TrimLeadingThink(final)
				if !printed {
					trimmed = strings.TrimLeft(trimmed, " \t\r\n")
				}
				fmt.Print(trimmed)
			}
		}
	}

	fmt.Println()
	sd.OnStreamEnd()

	if err := scanner.Err(); err != nil {
		return fullResponse.String(), err
	}

	return fullResponse.String(), nil
}

func (p *LlamaCliProvider) nonStreamResponse(stdout io.ReadCloser, stdin io.WriteCloser, cmd *exec.Cmd) (string, error) {
	defer stdin.Close()
	defer cmd.Wait()

	var buf bytes.Buffer
	idleTimeout := 1200 * time.Millisecond
	lastRead := time.Now()

	tmp := make([]byte, 4096)
	for {
		n, err := stdout.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
			lastRead = time.Now()
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", err
		}
		if time.Since(lastRead) > idleTimeout {
			break
		}
	}

	return buf.String(), nil
}
