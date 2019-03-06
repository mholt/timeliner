package oauth2client

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
)

// Browser gets an OAuth2 code via the web browser.
type Browser struct {
	// RedirectURL is the URL to redirect the browser
	// to after the code is obtained; it is usually a
	// loopback address. If empty, DefaultRedirectURL
	// will be used instead.
	RedirectURL string
}

// Get opens a browser window to authCodeURL for the user to
// authorize the application, and it returns the resulting
// OAuth2 code. It rejects requests where the "state" param
// does not match expectedStateVal.
func (b Browser) Get(expectedStateVal, authCodeURL string) (string, error) {
	redirURLStr := b.RedirectURL
	if redirURLStr == "" {
		redirURLStr = DefaultRedirectURL
	}
	redirURL, err := url.Parse(redirURLStr)
	if err != nil {
		return "", err
	}

	ln, err := net.Listen("tcp", redirURL.Host)
	if err != nil {
		return "", err
	}
	defer ln.Close()

	ch := make(chan string)
	errCh := make(chan error)

	go func() {
		handler := func(w http.ResponseWriter, r *http.Request) {
			state := r.FormValue("state")
			code := r.FormValue("code")

			if r.Method != "GET" || r.URL.Path != redirURL.Path || state == "" || code == "" {
				http.Error(w, "This endpoint is for OAuth2 callbacks only", http.StatusNotFound)
				return
			}

			if state != expectedStateVal {
				http.Error(w, "invalid state", http.StatusUnauthorized)
				errCh <- fmt.Errorf("invalid OAuth2 state; expected '%s' but got '%s'",
					expectedStateVal, state)
				return
			}

			fmt.Fprint(w, successBody)
			ch <- code
		}

		// must disable keep-alives, otherwise repeated calls to
		// this method can block indefinitely in some weird bug
		srv := http.Server{Handler: http.HandlerFunc(handler)}
		srv.SetKeepAlivesEnabled(false)
		srv.Serve(ln)
	}()

	err = openBrowser(authCodeURL)
	if err != nil {
		return "", err
	}

	select {
	case code := <-ch:
		return code, nil
	case err := <-errCh:
		return "", err
	}
}

// openBrowser opens the browser to url.
func openBrowser(url string) error {
	osCommand := map[string][]string{
		"darwin":  []string{"open"},
		"freebsd": []string{"xdg-open"},
		"linux":   []string{"xdg-open"},
		"netbsd":  []string{"xdg-open"},
		"openbsd": []string{"xdg-open"},
		"windows": []string{"cmd", "/c", "start"},
	}

	if runtime.GOOS == "windows" {
		// escape characters not allowed by cmd
		url = strings.Replace(url, "&", `^&`, -1)
	}

	all := osCommand[runtime.GOOS]
	exe := all[0]
	args := all[1:]

	buf := new(bytes.Buffer)

	cmd := exec.Command(exe, append(args, url)...)
	cmd.Stdout = buf
	cmd.Stderr = buf
	err := cmd.Run()

	if err != nil {
		return fmt.Errorf("%v: %s", err, buf.String())
	}

	return nil
}

const successBody = `<!DOCTYPE html>
<html>
	<head>
		<title>OAuth2 Success</title>
		<meta charset="utf-8">
		<style>
			body { text-align: center; padding: 5%; font-family: sans-serif; }
			h1 { font-size: 20px; }
			p { font-size: 16px; color: #444; }
		</style>
	</head>
	<body>
		<h1>Code obtained, thank you!</h1>
		<p>
			You may now close this page and return to the application.
		</p>
	</body>
</html>
`

var _ Getter = Browser{}
