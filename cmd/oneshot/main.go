// Oneshot CLI — single system+user prompt against a local Ollama model.
//
// Usage:
//
//	oneshot -model gemma4:e4b -system "..." "user prompt"
//	oneshot -model gpt-oss:20b -system @prompts/classify.md -user @email.txt
//	cat email.txt | oneshot -model gemma4:e4b -system "..."
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type options struct {
	Temperature float64 `json:"temperature"`
	NumCtx      int     `json:"num_ctx,omitempty"`
}

type request struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
	Stream   bool      `json:"stream"`
	Options  options   `json:"options"`
}

type response struct {
	Message message `json:"message"`
}

func main() {
	model := flag.String("model", "", "model name (required), e.g. gemma4:e4b, gpt-oss:20b")
	system := flag.String("system", "", "system prompt; prefix with @ to read from file")
	user := flag.String("user", "", "user prompt; prefix with @ to read from file. If empty, reads from positional args or stdin")
	host := flag.String("host", envOr("OLLAMA_HOST", "http://localhost:11434"), "Ollama base URL")
	temp := flag.Float64("temp", 0, "temperature (0 = deterministic)")
	numCtx := flag.Int("num-ctx", 0, "context window override (0 = model default)")
	asJSON := flag.Bool("json", false, "emit Ollama's full JSON response instead of just message content")
	flag.Parse()

	if *model == "" {
		fmt.Fprintln(os.Stderr, "error: -model is required")
		flag.Usage()
		os.Exit(2)
	}

	sysContent, err := loadValue(*system)
	if err != nil {
		fmt.Fprintf(os.Stderr, "system: %v\n", err)
		os.Exit(1)
	}

	userContent, err := loadUser(*user, flag.Args())
	if err != nil {
		fmt.Fprintf(os.Stderr, "user: %v\n", err)
		os.Exit(1)
	}
	if strings.TrimSpace(userContent) == "" {
		fmt.Fprintln(os.Stderr, "error: user prompt is empty (pass -user, positional arg, or pipe via stdin)")
		os.Exit(2)
	}

	msgs := []message{}
	if sysContent != "" {
		msgs = append(msgs, message{Role: "system", Content: sysContent})
	}
	msgs = append(msgs, message{Role: "user", Content: userContent})

	body, _ := json.Marshal(request{
		Model:    *model,
		Messages: msgs,
		Stream:   false,
		Options:  options{Temperature: *temp, NumCtx: *numCtx},
	})

	httpResp, err := http.Post(*host+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "request failed: %v\n", err)
		os.Exit(1)
	}
	defer httpResp.Body.Close()

	respBody, _ := io.ReadAll(httpResp.Body)
	if httpResp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "ollama error %d: %s\n", httpResp.StatusCode, respBody)
		os.Exit(1)
	}

	if *asJSON {
		os.Stdout.Write(respBody)
		fmt.Println()
		return
	}

	var resp response
	if err := json.Unmarshal(respBody, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "decode: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(resp.Message.Content)
}

func loadValue(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if strings.HasPrefix(s, "@") {
		b, err := os.ReadFile(s[1:])
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	return s, nil
}

func loadUser(flagVal string, args []string) (string, error) {
	if flagVal != "" {
		return loadValue(flagVal)
	}
	if len(args) > 0 {
		return strings.Join(args, " "), nil
	}
	fi, _ := os.Stdin.Stat()
	if (fi.Mode() & os.ModeCharDevice) == 0 {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	return "", nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
