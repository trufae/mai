package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"io"
	"os/exec"
	"sync"
	"time"

	"gioui.org/app"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"mcplib"
)

type ChatMessage struct {
	Text string
}

type ChatApp struct {
	th                 *material.Theme
	messages           []ChatMessage
	editor             widget.Editor
	actionBtn          widget.Clickable
	list               widget.List
	mcpBtn             widget.Clickable
	promptBtn          widget.Clickable
	settingsBtn        widget.Clickable
	showSettings       bool
	cmd                *exec.Cmd
	mcpClient          *mcplib.MCPClient
	running            bool
	waitingForResponse bool
	messageChan        chan ChatMessage
	ctx                context.Context
	cancel             context.CancelFunc
	mu                 sync.Mutex
	scrollToEnd        bool
}

func main() {
	th := material.NewTheme()

	chatApp := &ChatApp{
		th:       th,
		messages: []ChatMessage{{Text: "Welcome to MAI GUI!"}},
		list: widget.List{
			List: layout.List{
				Axis: layout.Vertical,
			},
		},
		messageChan: make(chan ChatMessage, 100),
	}

	go func() {
		var w app.Window
		w.Option(app.Title("MAI GUI"))
		var ops op.Ops

		for {
			e := w.Event()
			switch e := e.(type) {
			case app.FrameEvent:
				gtx := app.NewContext(&ops, e)
				chatApp.Layout(gtx)
				e.Frame(gtx.Ops)
			case app.DestroyEvent:
				return
			}
			// Check for new messages
			select {
			case msg := <-chatApp.messageChan:
				chatApp.messages = append(chatApp.messages, msg)
				chatApp.scrollToEnd = true
			default:
			}
		}
	}()

	app.Main()
}

func (c *ChatApp) Layout(gtx layout.Context) layout.Dimensions {
	if c.scrollToEnd {
		if len(c.messages) > 0 {
			c.list.Position.First = len(c.messages) - 1
			c.list.Position.Offset = 0
		}
		c.scrollToEnd = false
	}
	if c.showSettings {
		return layout.Flex{
			Axis: layout.Vertical,
		}.Layout(gtx,
			layout.Rigid(c.layoutTopBar),
			layout.Flexed(1, c.layoutSettings),
		)
	}
	return layout.Flex{
		Axis: layout.Vertical,
	}.Layout(gtx,
		layout.Rigid(c.layoutTopBar),
		layout.Flexed(1, c.layoutMessages),
		layout.Rigid(c.layoutInput),
	)
}

func (c *ChatApp) layoutTopBar(gtx layout.Context) layout.Dimensions {
	if c.settingsBtn.Clicked(gtx) {
		c.showSettings = !c.showSettings
	}
	return layout.Flex{
		Axis: layout.Horizontal,
	}.Layout(gtx,
		layout.Rigid(material.Button(c.th, &c.mcpBtn, "MCP").Layout),
		layout.Rigid(material.Button(c.th, &c.promptBtn, "Prompt").Layout),
		layout.Flexed(1, layout.Spacer{}.Layout),
		layout.Rigid(material.H6(c.th, "MAI Chat").Layout),
		layout.Flexed(1, layout.Spacer{}.Layout),
		layout.Rigid(material.Button(c.th, &c.settingsBtn, "Settings").Layout),
	)
}

func (c *ChatApp) layoutMessages(gtx layout.Context) layout.Dimensions {
	return c.list.Layout(gtx, len(c.messages), func(gtx layout.Context, index int) layout.Dimensions {
		return layout.Inset{
			Top:    4,
			Bottom: 4,
			Left:   8,
			Right:  8,
		}.Layout(gtx, material.Body1(c.th, c.messages[index].Text).Layout)
	})
}

func (c *ChatApp) sendUserMessage() {
	if text := c.editor.Text(); text != "" {
		if !c.running {
			c.startMAI()
		}
		c.messages = append(c.messages, ChatMessage{Text: "You: " + text})
		c.scrollToEnd = true
		c.editor.SetText("")
		c.waitingForResponse = true
		// Send to MAI via MCP
		if c.running {
			go c.sendMessage(text)
		}
	}
}

func (c *ChatApp) layoutInput(gtx layout.Context) layout.Dimensions {
	gtx.Constraints.Max.Y = 160
	gtx.Constraints.Min.Y = 60
	return layout.Inset{
		Top: 10, Bottom: 10, Left: 10, Right: 10,
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		defer clip.RRect{
			Rect: image.Rectangle{Max: gtx.Constraints.Max},
			SE:   10, SW: 10, NE: 10, NW: 10,
		}.Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, color.NRGBA{R: 240, G: 240, B: 240, A: 255})
		return layout.Inset{
			Top: 8, Bottom: 8, Left: 8, Right: 8,
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{
				Axis:    layout.Horizontal,
				Spacing: layout.SpaceEnd,
			}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					c.editor.Submit = true
					if ev, ok := c.editor.Update(gtx); ok {
						if _, submit := ev.(widget.SubmitEvent); submit {
							c.sendUserMessage()
						}
					}
					return material.Editor(c.th, &c.editor, "Type your message...").Layout(gtx)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					icon := "▶"
					if c.waitingForResponse {
						icon = "⏹"
					}
					btn := material.Button(c.th, &c.actionBtn, icon)
					if c.actionBtn.Clicked(gtx) {
						if c.waitingForResponse {
							c.cancel()
							c.running = false
							c.waitingForResponse = false
						} else {
							c.sendUserMessage()
						}
					}
					return btn.Layout(gtx)
				}),
			)
		})
	})
}

func (c *ChatApp) layoutSettings(gtx layout.Context) layout.Dimensions {
	return layout.Center.Layout(gtx, material.H6(c.th, "Settings Panel - TODO").Layout)
}

func (c *ChatApp) startMAI() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return
	}

	// Start mai in MCP mode
	c.cmd = exec.Command("../repl/mai-repl", "-M")
	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		c.addErrorMessage("Failed to create stdin pipe: " + err.Error())
		return
	}
	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		c.addErrorMessage("Failed to create stdout pipe: " + err.Error())
		return
	}

	stderr, err := c.cmd.StderrPipe()
	if err != nil {
		c.addErrorMessage("Failed to create stderr pipe: " + err.Error())
		return
	}

	// Create MCP client with the pipes
	c.mcpClient = mcplib.NewMCPClient()
	c.mcpClient.SetIO(stdout, stdin)

	// Handle stderr in background to prevent blocking
	go io.Copy(io.Discard, stderr)
	if err := c.cmd.Start(); err != nil {
		c.addErrorMessage("Failed to start MAI process: " + err.Error())
		return
	}

	// Give the process a moment to initialize
	time.Sleep(100 * time.Millisecond)

	// Initialize MCP connection
	if err := c.mcpClient.Initialize(); err != nil {
		c.addErrorMessage("Failed to initialize MCP: " + err.Error())
		c.cmd.Process.Kill()
		return
	}

	// Get available tools
	tools, err := c.mcpClient.ListTools()
	if err != nil {
		c.addErrorMessage("Failed to list tools: " + err.Error())
		c.cmd.Process.Kill()
		return
	}

	c.addErrorMessage(fmt.Sprintf("Connected to MAI MCP with %d tools", len(tools)))
	c.running = true

	// Start async response reader
	go c.readLoop()
}

func (c *ChatApp) addErrorMessage(msg string) {
	c.messageChan <- ChatMessage{Text: "Error: " + msg}
}

func (c *ChatApp) sendMessage(message string) {
	c.mu.Lock()
	client := c.mcpClient
	running := c.running
	c.mu.Unlock()

	if !running || client == nil {
		c.addErrorMessage("Not connected to MAI")
		return
	}

	// Call send_message tool
	result, err := client.CallTool("send_message", map[string]interface{}{
		"message": message,
		"stream":  false,
	})

	if err != nil {
		c.addErrorMessage("Failed to send message: " + err.Error())
		c.waitingForResponse = false
		return
	}

	if result.IsError {
		c.addErrorMessage("Tool call failed")
		c.waitingForResponse = false
		return
	}

	// Parse the response content
	if content, ok := result.Content.([]interface{}); ok && len(content) > 0 {
		if textContent, ok := content[0].(map[string]interface{}); ok {
			if text, ok := textContent["text"].(string); ok {
				c.messageChan <- ChatMessage{Text: "MAI: " + text}
			}
		}
	}
	c.waitingForResponse = false
}

func (c *ChatApp) readLoop() {
	// Wait for the process to finish
	err := c.cmd.Wait()
	c.mu.Lock()
	c.running = false
	c.mu.Unlock()

	if err != nil {
		c.addErrorMessage("MAI process exited: " + err.Error())
	} else {
		c.addErrorMessage("MAI process exited normally")
	}
}
