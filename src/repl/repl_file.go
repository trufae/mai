package main

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// registerFileCommands registers file and image handling commands
func registerFileCommands(r *REPL) {
	// File handling commands
	r.commands["/image"] = Command{
		Name:        "/image",
		Description: "Add an image to the next message",
		Handler: func(r *REPL, args []string) (string, error) {
			if len(args) < 2 {
				return "Usage: /image <path>\n\r", nil
			}
			return r.addImage(args[1])
		},
	}

	r.commands["/file"] = Command{
		Name:        "/file",
		Description: "Add a file to the next message",
		Handler: func(r *REPL, args []string) (string, error) {
			if len(args) < 2 {
				return "Usage: /file <path>\n\r", nil
			}
			return r.addFile(args[1])
		},
	}

	r.commands["/noimage"] = Command{
		Name:        "/noimage",
		Description: "Remove pending images",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.clearPendingImages()
		},
	}

	r.commands["/nofiles"] = Command{
		Name:        "/nofiles",
		Description: "Remove pending files",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.clearPendingFiles()
		},
	}
}

func (r *REPL) addImage(imagePath string) (string, error) {
	// Expand ~ to home directory
	if strings.HasPrefix(imagePath, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %v", err)
		}
		imagePath = filepath.Join(homeDir, imagePath[1:])
	}

	// Read image file
	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return "", fmt.Errorf("failed to read image: %v", err)
	}

	// Encode to base64 and build data URI
	encoded := base64.StdEncoding.EncodeToString(imageData)
	mimeType := http.DetectContentType(imageData)
	dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)

	// Add to pending files with data URI for image
	r.pendingFiles = append(r.pendingFiles, pendingFile{
		filePath: imagePath,
		isImage:  true,
		imageB64: dataURI,
	})

	r.addToHistory(fmt.Sprintf("/image %s", imagePath))
	message := fmt.Sprintf("Image added: %s (%d bytes). Send a message to analyze it.\r\n",
		filepath.Base(imagePath), len(imageData))
	return message, nil
}

func (r *REPL) addFile(filePath string) (string, error) {
	// Expand ~ to home directory
	if strings.HasPrefix(filePath, "~") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %v", err)
		}
		filePath = filepath.Join(homeDir, filePath[1:])
	}

	// Read file content
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %v", err)
	}

	// Add to pending files
	r.pendingFiles = append(r.pendingFiles, pendingFile{
		filePath: filePath,
		content:  string(content),
		isImage:  false,
	})

	r.addToHistory(fmt.Sprintf("/file %s", filePath))
	message := fmt.Sprintf("File added: %s (%d bytes). Send a message to analyze it.\r\n",
		filepath.Base(filePath), len(content))
	return message, nil
}

// clearPendingImages removes all pending images
func (r *REPL) clearPendingImages() (string, error) {
	imageCount := 0

	// Count images and remove them from pendingFiles
	var remainingFiles []pendingFile
	for _, file := range r.pendingFiles {
		if file.isImage {
			imageCount++
		} else {
			remainingFiles = append(remainingFiles, file)
		}
	}

	r.pendingFiles = remainingFiles

	var output strings.Builder
	if imageCount > 0 {
		output.WriteString(fmt.Sprintf("Removed %d pending image(s)\r\n", imageCount))
	} else {
		output.WriteString("No pending images to remove\r\n")
	}

	return output.String(), nil
}

// clearPendingFiles removes all pending non-image files
func (r *REPL) clearPendingFiles() (string, error) {
	fileCount := 0

	// Count regular files and remove them from pendingFiles
	var remainingFiles []pendingFile
	for _, file := range r.pendingFiles {
		if !file.isImage {
			fileCount++
		} else {
			remainingFiles = append(remainingFiles, file)
		}
	}

	r.pendingFiles = remainingFiles

	var output strings.Builder
	if fileCount > 0 {
		output.WriteString(fmt.Sprintf("Removed %d pending file(s)\r\n", fileCount))
	} else {
		output.WriteString("No pending files to remove\r\n")
	}

	return output.String(), nil
}
