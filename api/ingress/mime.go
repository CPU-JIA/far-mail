package ingress

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
)

type parsedMessage struct {
	Sender         string
	Subject        string
	BodyText       string
	BodyHTML       string
	HasAttachments bool
}

func parseMessage(raw []byte, envelopeSender string, maxBodyBytes int) parsedMessage {
	if maxBodyBytes <= 0 {
		maxBodyBytes = 262144
	}
	result := parsedMessage{Sender: clampRunes(envelopeSender, 320)}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		result.BodyText = clampBytesToString(raw, maxBodyBytes)
		return result
	}

	decoder := new(mime.WordDecoder)
	if subject := strings.TrimSpace(msg.Header.Get("Subject")); subject != "" {
		if decoded, err := decoder.DecodeHeader(subject); err == nil {
			result.Subject = clampRunes(strings.TrimSpace(decoded), 998)
		} else {
			result.Subject = clampRunes(subject, 998)
		}
	}
	if from := strings.TrimSpace(msg.Header.Get("From")); from != "" {
		if decoded, err := decoder.DecodeHeader(from); err == nil {
			from = decoded
		}
		if addrs, err := mail.ParseAddressList(from); err == nil && len(addrs) > 0 {
			result.Sender = clampRunes(strings.TrimSpace(addrs[0].Address), 320)
		} else {
			result.Sender = clampRunes(from, 320)
		}
	}
	if result.Sender == "" {
		result.Sender = clampRunes(envelopeSender, 320)
	}

	capacityHint := 0
	if len(raw) > 4096 {
		capacityHint = len(raw)
	}
	body, _ := readLimited(msg.Body, maxBodyBytes+1, capacityHint)
	contentType := msg.Header.Get("Content-Type")
	transferEncoding := msg.Header.Get("Content-Transfer-Encoding")
	collectBodyParts(contentType, transferEncoding, body, maxBodyBytes, &result)
	return result
}

func collectBodyParts(contentType string, transferEncoding string, body []byte, maxBodyBytes int, result *parsedMessage) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType == "" {
		mediaType = "text/plain"
	}
	mediaType = strings.ToLower(mediaType)

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return
		}
		reader := multipart.NewReader(bytes.NewReader(body), boundary)
		for {
			part, err := reader.NextPart()
			if err != nil {
				break
			}
			disposition := strings.ToLower(strings.TrimSpace(part.Header.Get("Content-Disposition")))
			_, partParams, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
			if strings.HasPrefix(disposition, "attachment") || strings.TrimSpace(partParams["name"]) != "" || strings.Contains(disposition, "filename=") {
				result.HasAttachments = true
			}
			partBody, _ := readLimited(part, maxBodyBytes+1, 0)
			collectBodyParts(part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding"), partBody, maxBodyBytes, result)
			_ = part.Close()
			if len(result.BodyText) >= maxBodyBytes && len(result.BodyHTML) >= maxBodyBytes {
				break
			}
		}
		return
	}

	decoded := decodeTransfer(body, transferEncoding, maxBodyBytes)
	switch mediaType {
	case "text/plain":
		appendBody(&result.BodyText, decoded, maxBodyBytes)
	case "text/html":
		appendBody(&result.BodyHTML, decoded, maxBodyBytes)
	default:
		if result.BodyText == "" && !strings.HasPrefix(mediaType, "application/") && !strings.HasPrefix(mediaType, "image/") {
			appendBody(&result.BodyText, decoded, maxBodyBytes)
		}
	}
}

func decodeTransfer(body []byte, transferEncoding string, maxBytes int) string {
	transferEncoding = strings.ToLower(strings.TrimSpace(transferEncoding))
	var reader io.Reader
	switch transferEncoding {
	case "base64":
		reader = base64.NewDecoder(base64.StdEncoding, bytes.NewReader(body))
	case "quoted-printable":
		reader = quotedprintable.NewReader(bytes.NewReader(body))
	default:
		// 7bit/8bit/binary and omitted encodings already contain the decoded
		// body. Avoid copying the entire message through another bytes.Reader +
		// io.ReadAll cycle on the overwhelmingly common path.
		return clampBytesToString(body, maxBytes)
	}
	decoded, err := readLimited(reader, maxBytes+1, len(body))
	if err != nil {
		decoded = body
	}
	return clampBytesToString(decoded, maxBytes)
}

func readLimited(reader io.Reader, limit, capacityHint int) ([]byte, error) {
	if limit <= 0 {
		return []byte{}, nil
	}
	if capacityHint < 0 {
		capacityHint = 0
	}
	if capacityHint > 0 && capacityHint < 512 {
		capacityHint = 512
	}
	if capacityHint > limit {
		capacityHint = limit
	}
	buffer := bytes.NewBuffer(make([]byte, 0, capacityHint))
	_, err := io.Copy(buffer, io.LimitReader(reader, int64(limit)))
	return buffer.Bytes(), err
}

func appendBody(target *string, value string, maxBytes int) {
	value = strings.TrimSpace(value)
	if value == "" || len(*target) >= maxBytes {
		return
	}
	if *target == "" {
		*target = clampStringBytes(value, maxBytes)
		return
	}
	if *target != "" {
		*target += "\n"
	}
	remaining := maxBytes - len(*target)
	if remaining <= 0 {
		return
	}
	*target += clampStringBytes(value, remaining)
}

func clampBytesToString(value []byte, maxBytes int) string {
	if maxBytes > 0 && len(value) > maxBytes {
		value = value[:maxBytes]
	}
	return strings.ToValidUTF8(string(value), "")
}

func clampStringBytes(value string, maxBytes int) string {
	if maxBytes > 0 && len(value) > maxBytes {
		value = value[:maxBytes]
	}
	return strings.ToValidUTF8(value, "")
}

func clampRunes(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if maxRunes <= 0 {
		return value
	}
	count := 0
	for idx := range value {
		if count == maxRunes {
			return value[:idx]
		}
		count++
	}
	return value
}
