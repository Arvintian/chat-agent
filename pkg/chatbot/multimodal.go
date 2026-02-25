package chatbot

import (
	"strings"

	"github.com/cloudwego/eino/schema"
)

// FileData represents file data for multimodal messages
type FileData struct {
	URL      string
	Type     string
	Name     string
	FileSize int64
}

// createMultimodalUserMessage creates a user message with text and files
// Supports image, audio, and video file types.
// Other file types (PDF, Word, Excel, etc.) are skipped as they require text extraction.
func createMultimodalUserMessage(text string, files []FileData) *schema.Message {
	// If no files, return simple text message
	if len(files) == 0 {
		return schema.UserMessage(text)
	}

	// Build input parts for multimodal message using UserInputMultiContent
	inputParts := make([]schema.MessageInputPart, 0, 1+len(files))

	// Add text part if present
	if text != "" {
		inputParts = append(inputParts, schema.MessageInputPart{
			Type: schema.ChatMessagePartTypeText,
			Text: text,
		})
	}

	// Process all file types based on MIME type
	for _, file := range files {
		var part schema.MessageInputPart

		// Parse data URL to extract base64 data and MIME type if applicable
		// For data URL format 'data:[<mediatype>][;base64],<data>', use Base64Data and MIMEType fields
		// For HTTP/HTTPS URLs, use URL field directly
		common := schema.MessagePartCommon{}
		if strings.HasPrefix(file.URL, "data:") {
			// Extract MIME type and base64 data from data URL
			mimeType, base64Data := parseDataURL(file.URL)
			if mimeType != "" && base64Data != "" {
				common.MIMEType = mimeType
				common.Base64Data = &base64Data
			} else {
				// Fallback to URL if parsing fails
				common.URL = &file.URL
			}
		} else {
			// Use URL directly for HTTP/HTTPS links
			common.URL = &file.URL
		}

		switch {
		case strings.HasPrefix(file.Type, "image/"):
			part = schema.MessageInputPart{
				Type: schema.ChatMessagePartTypeImageURL,
				Image: &schema.MessageInputImage{
					MessagePartCommon: common,
				},
			}
		case strings.HasPrefix(file.Type, "audio/"):
			part = schema.MessageInputPart{
				Type: schema.ChatMessagePartTypeAudioURL,
				Audio: &schema.MessageInputAudio{
					MessagePartCommon: common,
				},
			}
		case strings.HasPrefix(file.Type, "video/"):
			part = schema.MessageInputPart{
				Type: schema.ChatMessagePartTypeVideoURL,
				Video: &schema.MessageInputVideo{
					MessagePartCommon: common,
				},
			}
		default:
			// Handle other file types (PDF, Word, Excel, etc.) using file_url type
			part = schema.MessageInputPart{
				Type: schema.ChatMessagePartTypeFileURL,
				File: &schema.MessageInputFile{
					MessagePartCommon: common,
					Name:              file.Name,
				},
			}
		}

		inputParts = append(inputParts, part)
	}

	// Create message with multimodal content
	msg := schema.UserMessage("")
	msg.UserInputMultiContent = inputParts

	return msg
}

// parseDataURL extracts MIME type and base64 data from a data URL
// Format: data:[<mediatype>][;base64],<data>
// Returns mimeType and base64Data, or empty strings if parsing fails
func parseDataURL(dataURL string) (mimeType, base64Data string) {
	if !strings.HasPrefix(dataURL, "data:") {
		return "", ""
	}

	commaIndex := strings.Index(dataURL, ",")
	if commaIndex == -1 {
		return "", ""
	}

	metadata := dataURL[5:commaIndex]
	base64Data = dataURL[commaIndex+1:]

	semicolonIndex := strings.Index(metadata, ";")
	if semicolonIndex != -1 {
		mimeType = metadata[:semicolonIndex]
	} else {
		mimeType = metadata
	}

	if !strings.Contains(mimeType, "/") {
		return "", ""
	}

	return mimeType, base64Data
}
