package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadData loads data from the given path into the provided callback function.
// The callback is called for each piece of data (line or parsed section).
func LoadData(path string, minChars int, callback func(string)) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() {
				return loadFile(p, minChars, callback)
			}
			return nil
		})
	} else {
		return loadFile(path, minChars, callback)
	}
}

// loadFile loads a single file based on its extension.
func loadFile(path string, minChars int, callback func(string)) error {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".txt", ".csv":
		return loadTextFile(path, callback)
	case ".md":
		return loadMarkdownFile(path, callback)
	default:
		// Skip unknown extensions
		return nil
	}
}

// loadTextFile reads each line from .txt or .csv files.
func loadTextFile(path string, callback func(string)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			callback(line)
		}
	}
	return scanner.Err()
}

// loadMarkdownFile parses .md files and extracts sections.
func loadMarkdownFile(path string, callback func(string)) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var currentTitle, currentSection, currentSubsection string
	var sectionText strings.Builder
	var inSection bool

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "# ") {
			// New title
			if inSection {
				callback(buildSectionString(currentTitle, currentSection, currentSubsection, sectionText.String()))
				sectionText.Reset()
			}
			currentTitle = strings.TrimPrefix(trimmed, "# ")
			currentSection = ""
			currentSubsection = ""
			inSection = true
		} else if strings.HasPrefix(trimmed, "## ") {
			// New section
			if inSection {
				callback(buildSectionString(currentTitle, currentSection, currentSubsection, sectionText.String()))
				sectionText.Reset()
			}
			currentSection = strings.TrimPrefix(trimmed, "## ")
			currentSubsection = ""
			inSection = true
		} else if strings.HasPrefix(trimmed, "### ") {
			// New subsection
			if inSection {
				callback(buildSectionString(currentTitle, currentSection, currentSubsection, sectionText.String()))
				sectionText.Reset()
			}
			currentSubsection = strings.TrimPrefix(trimmed, "### ")
			inSection = true
		} else if trimmed == "" {
			// Empty line, add to text if in section
			if inSection {
				sectionText.WriteString("\n")
			}
		} else {
			// Content line
			if inSection {
				sectionText.WriteString(line + "\n")
			}
		}
	}

	// Last section
	if inSection {
		callback(buildSectionString(currentTitle, currentSection, currentSubsection, sectionText.String()))
	}

	return scanner.Err()
}

// buildSectionString builds the string for a section.
func buildSectionString(title, section, subsection, text string) string {
	var parts []string
	if title != "" {
		parts = append(parts, title)
	}
	if section != "" {
		parts = append(parts, section)
	}
	if subsection != "" {
		parts = append(parts, subsection)
	}
	header := strings.Join(parts, "->")
	return fmt.Sprintf("%s: %s", header, strings.TrimSpace(text))
}
