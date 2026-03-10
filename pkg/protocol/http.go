package protocol

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	ErrTooShort      = errors.New("data too short")
	ErrNotHTTP       = errors.New("not HTTP data")
	ErrInvalidFormat = errors.New("invalid format")
)

type HTTPMethod int

const (
	MethodUnknown HTTPMethod = iota
	MethodGET
	MethodPOST
	MethodPUT
	MethodDELETE
	MethodPATCH
	MethodHEAD
	MethodOPTIONS
)

func (m HTTPMethod) String() string {
	switch m {
	case MethodGET:
		return "GET"
	case MethodPOST:
		return "POST"
	case MethodPUT:
		return "PUT"
	case MethodDELETE:
		return "DELETE"
	case MethodPATCH:
		return "PATCH"
	case MethodHEAD:
		return "HEAD"
	case MethodOPTIONS:
		return "OPTIONS"
	default:
		return "UNKNOWN"
	}
}

type HTTPRequest struct {
	Method  HTTPMethod
	Path    string
	Version string
	Headers map[string]string
	Body    []byte
	Raw     []byte
}

type HTTPResponse struct {
	Version string
	Status  int
	Message string
	Headers map[string]string
	Body    []byte
	Raw     []byte
}

func ParseRequest(data []byte) (*HTTPRequest, error) {
	if len(data) < 4 {
		return nil, ErrTooShort
	}

	method, path, version, err := parseRequestLine(data)
	if err != nil {
		return nil, err
	}

	req := &HTTPRequest{
		Method:  method,
		Path:    path,
		Version: version,
		Headers: make(map[string]string),
		Raw:     data,
	}

	headerEnd := bytes.Index(data, []byte("\r\n\r\n"))
	if headerEnd > 0 {
		headerData := data[:headerEnd]
		lines := strings.Split(string(headerData), "\r\n")
		for i := 1; i < len(lines); i++ {
			line := lines[i]
			colonIdx := strings.Index(line, ":")
			if colonIdx > 0 {
				key := strings.TrimSpace(line[:colonIdx])
				value := strings.TrimSpace(line[colonIdx+1:])
				req.Headers[key] = value
			}
		}

		if headerEnd+4 < len(data) {
			req.Body = data[headerEnd+4:]
		}
	}

	return req, nil
}

func parseRequestLine(data []byte) (HTTPMethod, string, string, error) {
	sp1 := bytes.Index(data, []byte(" "))
	if sp1 < 0 {
		return MethodUnknown, "", "", ErrInvalidFormat
	}

	methodStr := string(data[:sp1])
	method := parseMethod(methodStr)

	sp2 := bytes.Index(data[sp1+1:], []byte(" "))
	if sp2 < 0 {
		return MethodUnknown, "", "", ErrInvalidFormat
	}
	sp2 += sp1 + 1

	path := string(data[sp1+1 : sp2])
	version := string(data[sp2+1:])

	if !strings.HasPrefix(version, "HTTP/") {
		return MethodUnknown, "", "", ErrInvalidFormat
	}

	return method, path, version, nil
}

func parseMethod(s string) HTTPMethod {
	switch s {
	case "GET":
		return MethodGET
	case "POST":
		return MethodPOST
	case "PUT":
		return MethodPUT
	case "DELETE":
		return MethodDELETE
	case "PATCH":
		return MethodPATCH
	case "HEAD":
		return MethodHEAD
	case "OPTIONS":
		return MethodOPTIONS
	default:
		return MethodUnknown
	}
}

func ParseResponse(data []byte) (*HTTPResponse, error) {
	if len(data) < 12 {
		return nil, ErrTooShort
	}

	if !bytes.HasPrefix(data, []byte("HTTP/")) {
		return nil, ErrNotHTTP
	}

	space1 := bytes.Index(data, []byte(" "))
	if space1 < 0 || space1 < 6 {
		return nil, ErrInvalidFormat
	}

	space2 := bytes.Index(data[space1+1:], []byte(" "))
	if space2 < 0 {
		return nil, ErrInvalidFormat
	}
	space2 += space1 + 1

	version := string(data[:space1])
	statusStr := string(data[space1+1 : space2])
	message := string(data[space2+1:])

	status, err := strconv.Atoi(statusStr)
	if err != nil {
		return nil, ErrInvalidFormat
	}

	resp := &HTTPResponse{
		Version: version,
		Status:  status,
		Message: message,
		Headers: make(map[string]string),
		Raw:     data,
	}

	headerEnd := bytes.Index(data, []byte("\r\n\r\n"))
	if headerEnd > 0 {
		headerData := data[:headerEnd]
		lines := strings.Split(string(headerData), "\r\n")

		for i := 1; i < len(lines); i++ {
			line := lines[i]
			colonIdx := strings.Index(line, ":")
			if colonIdx > 0 {
				key := strings.TrimSpace(line[:colonIdx])
				value := strings.TrimSpace(line[colonIdx+1:])
				resp.Headers[key] = value
			}
		}

		if headerEnd+4 < len(data) {
			resp.Body = data[headerEnd+4:]
		}
	}

	return resp, nil
}

func IsHTTP(data []byte) bool {
	if len(data) < 4 {
		return false
	}

	methods := []string{"GET ", "POST", "PUT ", "DELE", "HEAD", "PATC", "OPTI"}
	for _, m := range methods {
		if bytes.HasPrefix(data, []byte(m)) {
			return true
		}
	}

	if bytes.HasPrefix(data, []byte("HTTP/")) {
		return true
	}

	return false
}

func ExtractPath(data []byte) string {
	req, err := ParseRequest(data)
	if err != nil {
		return ""
	}
	return req.Path
}

func ExtractStatus(data []byte) int {
	resp, err := ParseResponse(data)
	if err != nil {
		return 0
	}
	return resp.Status
}

func ContentLength(h map[string]string) int64 {
	cl, ok := h["Content-Length"]
	if !ok {
		cl, ok = h["content-length"]
	}
	if !ok {
		return 0
	}
	n, _ := strconv.ParseInt(cl, 10, 64)
	return n
}

func NormalizePath(path string) string {
	path = strings.Split(path, "?")[0]
	path = strings.Split(path, "#")[0]
	if len(path) > 100 {
		path = path[:100]
	}
	return path
}

func GetPathTemplate(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "" {
			continue
		}
		if _, err := strconv.Atoi(part); err == nil {
			parts[i] = ":num"
		} else if strings.Contains(part, "-") && strings.Contains(part, ".") {
			parts[i] = ":uuid"
		}
	}
	return strings.Join(parts, "/")
}

func FormatHTTPMethod(m HTTPMethod) string {
	return m.String()
}

func ParseHTTPVersion(v string) (int, int, error) {
	v = strings.TrimPrefix(v, "HTTP/")
	parts := strings.Split(v, ".")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid HTTP version")
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	return major, minor, nil
}
