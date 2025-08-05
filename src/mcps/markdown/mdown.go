package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"regexp"
	"strings"

	"mcplib"
)

// Section represents a markdown header section
type Section struct {
	Name      string
	Level     int
	StartLine int
	EndLine   int
}

// MDownService provides markdown MCP tools
type MDownService struct {
	sections       []Section
	sectionContent map[string]string
}

var mdPath string

func init() {
	flag.Parse()
	args := flag.Args()
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: mai mcp <markdown-file>")
		os.Exit(1)
	}
	mdPath = args[0]
}

// NewMDownService creates a service by parsing the markdown file
func NewMDownService() *MDownService {
	data, err := ioutil.ReadFile(mdPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read markdown file: %v\n", err)
		os.Exit(1)
	}
	content := string(data)
	lines := strings.Split(content, "\n")
	headerRe := regexp.MustCompile(`^(#{1,6})\s*(.+)$`)

	var sections []Section
	for i, line := range lines {
		if m := headerRe.FindStringSubmatch(line); m != nil {
			sections = append(sections, Section{
				Name:      m[2],
				Level:     len(m[1]),
				StartLine: i,
				EndLine:   len(lines),
			})
		}
	}
	for i := range sections {
		end := len(lines)
		for j := i + 1; j < len(sections); j++ {
			if sections[j].Level <= sections[i].Level {
				end = sections[j].StartLine
				break
			}
		}
		sections[i].EndLine = end
	}

	contentMap := make(map[string]string)
	for _, sec := range sections {
		snippet := lines[sec.StartLine+1 : sec.EndLine]
		contentMap[sec.Name] = strings.Join(snippet, "\n")
	}
	return &MDownService{sections: sections, sectionContent: contentMap}
}

// GetTools returns the static markdown MCP tools
func (s *MDownService) GetTools() []mcplib.Tool {
	return []mcplib.Tool{
		{
			Name:        "list_sections",
			Description: "Enumerates all sections within the markdown document.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Handler:     s.handleListSections,
		},
		{
			Name:        "get_contents",
			Description: "Retrieves the full content of a given section.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"section": map[string]any{"type": "string", "description": "Name of the section"},
				},
				"required": []string{"section"},
			},
			Handler: s.handleGetContents,
		},
		{
			Name:        "show_contents",
			Description: "Outputs the content of a specified section directly to the console.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"section": map[string]any{"type": "string", "description": "Name of the section"},
				},
				"required": []string{"section"},
			},
			Handler: s.handleShowContents,
		},
		{
			Name:        "show_tree",
			Description: "Displays a hierarchical tree representation of the document’s structure.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Handler:     s.handleShowTree,
		},
		{
			Name:        "search",
			Description: "Locates and returns sections that correspond to a search query or list of search queries.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":   map[string]any{"type": "string", "description": "Search query (single term or phrase)"},
					"queries": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "List of search queries"},
				},
				"oneOf": []map[string]any{
					{"required": []string{"query"}},
					{"required": []string{"queries"}},
				},
			},
			Handler: s.handleSearch,
		},
		{
			Name:        "summarize",
			Description: "Generates a concise summary of a specified section.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"section": map[string]any{"type": "string", "description": "Name of the section"},
				},
				"required": []string{"section"},
			},
			Handler: s.handleSummarize,
		},
	}
}

func (s *MDownService) handleListSections(args map[string]any) (any, error) {
	var out []map[string]any
	for _, sec := range s.sections {
		out = append(out, map[string]any{"name": sec.Name, "level": sec.Level})
	}
	return map[string]any{"sections": out}, nil
}

func (s *MDownService) handleGetContents(args map[string]any) (any, error) {
	name, ok := args["section"].(string)
	if !ok || name == "" {
		return nil, fmt.Errorf("section is required")
	}
	content, ok := s.sectionContent[name]
	if !ok {
		return nil, fmt.Errorf("section %q not found", name)
	}
	return map[string]any{"content": content}, nil
}

func (s *MDownService) handleShowContents(args map[string]any) (any, error) {
	res, err := s.handleGetContents(args)
	if err != nil {
		return nil, err
	}
	content := res.(map[string]any)["content"].(string)
	fmt.Println(content)
	return nil, nil
}

func (s *MDownService) handleShowTree(args map[string]any) (any, error) {
	var b strings.Builder
	for _, sec := range s.sections {
		indent := strings.Repeat("  ", sec.Level-1)
		b.WriteString(fmt.Sprintf("%s- %s\n", indent, sec.Name))
	}
	return map[string]any{"tree": b.String()}, nil
}

func (s *MDownService) handleSearch(args map[string]any) (any, error) {
	var terms []string
	if q, ok := args["query"].(string); ok && q != "" {
		terms = append(terms, q)
	}
	if qs, ok := args["queries"].([]any); ok {
		for _, v := range qs {
			if s, ok := v.(string); ok && s != "" {
				terms = append(terms, s)
			}
		}
	}
	if len(terms) == 0 {
		return nil, fmt.Errorf("query or queries is required")
	}
	var matches []string
	for name, content := range s.sectionContent {
		for _, term := range terms {
			if strings.Contains(name, term) || strings.Contains(content, term) {
				matches = append(matches, name)
				break
			}
		}
	}
	return map[string]any{"sections": matches}, nil
}

func (s *MDownService) handleSummarize(args map[string]any) (any, error) {
	name, ok := args["section"].(string)
	if !ok || name == "" {
		return nil, fmt.Errorf("section is required")
	}
	content, ok := s.sectionContent[name]
	if !ok {
		return nil, fmt.Errorf("section %q not found", name)
	}
	sentences := regexp.MustCompile(`(?m)([^\.!?]+[\.!?])`).FindAllString(content, -1)
	var summary string
	if len(sentences) > 0 {
		limit := 3
		if len(sentences) < limit {
			limit = len(sentences)
		}
		summary = strings.Join(sentences[:limit], " ")
	} else if len(content) > 200 {
		summary = content[:200] + "..."
	} else {
		summary = content
	}
	return map[string]any{"summary": summary}, nil
}
