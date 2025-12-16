package mcplib

import (
	"fmt"
	"reflect"
	"strings"
)

// ApplyPromptDefinition renders a prompt definition with the provided arguments.
func ApplyPromptDefinition(prompt PromptDefinition, args map[string]any) ([]Message, error) {
	if args == nil {
		args = make(map[string]any)
	}
	result := make([]Message, 0, len(prompt.Messages))
	for _, msg := range prompt.Messages {
		out := Message{Role: msg.Role}
		for _, c := range msg.Content {
			if c.Type != "text" || c.Text == "" {
				out.Content = append(out.Content, c)
				continue
			}
			rendered, err := renderTemplateString(c.Text, args)
			if err != nil {
				return nil, err
			}
			out.Content = append(out.Content, Content{Type: "text", Text: rendered})
		}
		result = append(result, out)
	}
	return result, nil
}

type templateNode interface {
	render(*renderContext) (string, error)
}

type textNode struct {
	text string
}

func (n textNode) render(_ *renderContext) (string, error) {
	return n.text, nil
}

type varNode struct {
	name string
}

func (n varNode) render(ctx *renderContext) (string, error) {
	val, _ := ctx.lookup(n.name)
	return toString(val), nil
}

type ifNode struct {
	name        string
	trueBranch  []templateNode
	falseBranch []templateNode
}

func (n ifNode) render(ctx *renderContext) (string, error) {
	val, _ := ctx.lookup(n.name)
	branch := n.falseBranch
	if isTruthy(val) {
		branch = n.trueBranch
	}
	return renderNodes(branch, ctx)
}

type eachNode struct {
	name     string
	children []templateNode
}

func (n eachNode) render(ctx *renderContext) (string, error) {
	val, _ := ctx.lookup(n.name)
	items := iterable(val)
	if len(items) == 0 {
		return "", nil
	}
	var builder strings.Builder
	for _, item := range items {
		childCtx := &renderContext{value: item, parent: ctx}
		chunk, err := renderNodes(n.children, childCtx)
		if err != nil {
			return "", err
		}
		builder.WriteString(chunk)
	}
	return builder.String(), nil
}

type renderContext struct {
	value  any
	parent *renderContext
}

func (ctx *renderContext) lookup(name string) (any, bool) {
	if name == "this" || name == "." {
		return ctx.value, true
	}
	for c := ctx; c != nil; c = c.parent {
		if val, ok := lookupInValue(c.value, name); ok {
			return val, true
		}
	}
	return nil, false
}

func lookupInValue(val any, name string) (any, bool) {
	switch typed := val.(type) {
	case map[string]interface{}:
		v, ok := typed[name]
		return v, ok
	}
	rv := reflect.ValueOf(val)
	for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, false
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Map:
		key := reflect.ValueOf(name)
		if !key.IsValid() {
			return nil, false
		}
		if key.Type() != rv.Type().Key() {
			if key.Type().ConvertibleTo(rv.Type().Key()) {
				key = key.Convert(rv.Type().Key())
			} else {
				return nil, false
			}
		}
		val := rv.MapIndex(key)
		if !val.IsValid() {
			return nil, false
		}
		return val.Interface(), true
	case reflect.Struct:
		field := rv.FieldByName(name)
		if field.IsValid() && field.CanInterface() {
			return field.Interface(), true
		}
	}
	return nil, false
}

func iterable(val any) []any {
	rv := reflect.ValueOf(val)
	for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		items := make([]any, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			items = append(items, rv.Index(i).Interface())
		}
		return items
	case reflect.Map:
		// Iterate values for map inputs to maintain compatibility with loose Handlebars semantics.
		keys := rv.MapKeys()
		items := make([]any, 0, len(keys))
		for _, k := range keys {
			items = append(items, rv.MapIndex(k).Interface())
		}
		return items
	}
	return nil
}

func isTruthy(val any) bool {
	if val == nil {
		return false
	}
	switch v := val.(type) {
	case bool:
		return v
	case string:
		return v != ""
	}
	rv := reflect.ValueOf(val)
	for rv.Kind() == reflect.Interface || rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return false
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Bool:
		return rv.Bool()
	case reflect.String:
		return rv.Len() > 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return rv.Uint() != 0
	case reflect.Float32, reflect.Float64:
		return rv.Float() != 0
	case reflect.Slice, reflect.Array, reflect.Map:
		return rv.Len() > 0
	case reflect.Struct:
		return true
	}
	return true
}

func toString(val any) string {
	if val == nil {
		return ""
	}
	switch v := val.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case fmt.GoStringer:
		return v.GoString()
	}
	return fmt.Sprint(val)
}

func renderTemplateString(template string, args map[string]any) (string, error) {
	nodes, err := parseTemplate(template)
	if err != nil {
		return "", err
	}
	root := &renderContext{value: args}
	return renderNodes(nodes, root)
}

func renderNodes(nodes []templateNode, ctx *renderContext) (string, error) {
	var builder strings.Builder
	for _, n := range nodes {
		chunk, err := n.render(ctx)
		if err != nil {
			return "", err
		}
		builder.WriteString(chunk)
	}
	return builder.String(), nil
}

func parseTemplate(input string) ([]templateNode, error) {
	parser := templateParser{input: input}
	nodes, endTag, err := parser.parseNodesUntil(nil)
	if err != nil {
		return nil, err
	}
	if endTag != "" {
		return nil, fmt.Errorf("unexpected closing tag %s", endTag)
	}
	return nodes, nil
}

type templateParser struct {
	input string
	pos   int
}

func (p *templateParser) parseNodesUntil(endTags map[string]bool) ([]templateNode, string, error) {
	var nodes []templateNode
	for p.pos < len(p.input) {
		if strings.HasPrefix(p.input[p.pos:], "{{") {
			tag, err := p.readTag()
			if err != nil {
				return nil, "", err
			}
			if endTags != nil {
				if endTags[tag] {
					return nodes, tag, nil
				}
				if tag == "else" && endTags["else"] {
					return nodes, tag, nil
				}
			}
			switch {
			case tag == "else":
				return nil, "", fmt.Errorf("unexpected else outside of if section")
			case strings.HasPrefix(tag, "#if "):
				name := strings.TrimSpace(tag[4:])
				if name == "" {
					return nil, "", fmt.Errorf("empty identifier in if section")
				}
				trueNodes, terminal, err := p.parseNodesUntil(map[string]bool{"else": true, "/if": true})
				if err != nil {
					return nil, "", err
				}
				var falseNodes []templateNode
				if terminal == "else" {
					falseNodes, terminal, err = p.parseNodesUntil(map[string]bool{"/if": true})
					if err != nil {
						return nil, "", err
					}
				}
				if terminal != "/if" {
					return nil, "", fmt.Errorf("missing closing tag for if %s", name)
				}
				nodes = append(nodes, ifNode{name: name, trueBranch: trueNodes, falseBranch: falseNodes})
			case strings.HasPrefix(tag, "#each "):
				name := strings.TrimSpace(tag[6:])
				if name == "" {
					return nil, "", fmt.Errorf("empty identifier in each section")
				}
				children, terminal, err := p.parseNodesUntil(map[string]bool{"/each": true})
				if err != nil {
					return nil, "", err
				}
				if terminal != "/each" {
					return nil, "", fmt.Errorf("missing closing tag for each %s", name)
				}
				nodes = append(nodes, eachNode{name: name, children: children})
			case strings.HasPrefix(tag, "/"):
				return nil, "", fmt.Errorf("unexpected closing tag %s", tag)
			default:
				nodes = append(nodes, varNode{name: tag})
			}
		} else {
			text := p.readText()
			if text != "" {
				nodes = append(nodes, textNode{text: text})
			}
		}
	}
	if len(endTags) > 0 {
		var expected []string
		for tag := range endTags {
			expected = append(expected, tag)
		}
		return nil, "", fmt.Errorf("unterminated template section; expected %s", strings.Join(expected, " or "))
	}
	return nodes, "", nil
}

func (p *templateParser) readText() string {
	start := p.pos
	idx := strings.Index(p.input[start:], "{{")
	if idx < 0 {
		p.pos = len(p.input)
		return p.input[start:]
	}
	if idx == 0 {
		return ""
	}
	p.pos = start + idx
	return p.input[start:p.pos]
}

func (p *templateParser) readTag() (string, error) {
	if !strings.HasPrefix(p.input[p.pos:], "{{") {
		return "", fmt.Errorf("expected '{{' at position %d", p.pos)
	}
	start := p.pos + 2
	idx := strings.Index(p.input[start:], "}}")
	if idx < 0 {
		return "", fmt.Errorf("unterminated tag starting at position %d", p.pos)
	}
	content := p.input[start : start+idx]
	p.pos = start + idx + 2
	return strings.TrimSpace(content), nil
}
