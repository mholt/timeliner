package oauth2proxy

import (
	"encoding/json"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/mholt/timeliner/oauth2client"
	"golang.org/x/oauth2"
)

// New returns a new OAuth2 proxy that serves its endpoints
// under the given basePath and which replaces credentials
// and endpoints with those found in the configs given in
// the providers map.
//
// The map value does not use pointers, so that temporary
// manipulations of the value can occur without modifying
// the original template value.
func New(basePath string, providers map[string]oauth2.Config) http.Handler {
	basePath = path.Join("/", basePath)

	proxy := oauth2Proxy{providers: providers}

	mux := http.NewServeMux()
	mux.HandleFunc(path.Join(basePath, "auth-code-url"), proxy.handleAuthCodeURL)
	mux.HandleFunc(path.Join(basePath, "proxy")+"/", proxy.handleOAuth2)

	return mux
}

type oauth2Proxy struct {
	providers map[string]oauth2.Config
}

func (proxy oauth2Proxy) handleAuthCodeURL(w http.ResponseWriter, r *http.Request) {
	providerID := r.FormValue("provider")
	redir := r.FormValue("redirect")
	scopes := r.URL.Query()["scope"]

	oauth2CfgCopy, ok := proxy.providers[providerID]
	if !ok {
		http.Error(w, "unknown service ID", http.StatusBadRequest)
		return
	}

	// augment the template config with parameters specific to this
	// request (this is why it's important that the configs aren't
	// pointers; we should be mutating only copies here)
	oauth2CfgCopy.Scopes = scopes
	oauth2CfgCopy.RedirectURL = redir

	stateVal := oauth2client.State()
	url := oauth2CfgCopy.AuthCodeURL(stateVal, oauth2.AccessTypeOffline)

	info := oauth2client.OAuth2Info{
		StateValue:  stateVal,
		AuthCodeURL: url,
	}

	json.NewEncoder(w).Encode(info)
}

func (proxy oauth2Proxy) handleOAuth2(w http.ResponseWriter, r *http.Request) {
	// knead the URL into its two parts: the service
	// ID and which endpoint to proxy to
	// reqURL := strings.TrimPrefix(r.URL.Path, basePath+"/proxy")
	// reqURL = path.Clean(strings.TrimPrefix(reqURL, "/"))

	// we want the last two components of the path
	urlParts := strings.Split(r.URL.Path, "/")
	if len(urlParts) < 2 {
		http.Error(w, "bad path length", http.StatusBadRequest)
		return
	}

	providerID := urlParts[len(urlParts)-2]
	whichEndpoint := urlParts[len(urlParts)-1]

	// get the OAuth2 config matching the service ID
	oauth2Config, ok := proxy.providers[providerID]
	if !ok {
		http.Error(w, "unknown service: "+providerID, http.StatusBadRequest)
		return
	}

	// figure out which endpoint we'll use for upstream
	var upstreamEndpoint string
	switch whichEndpoint {
	case "auth":
		upstreamEndpoint = oauth2Config.Endpoint.AuthURL
	case "token":
		upstreamEndpoint = oauth2Config.Endpoint.TokenURL
	}

	// read the body so we can replace values if necessary
	// (don't use r.ParseForm because we need to keep body
	// and query string distinct)
	reqBodyBytes, err := ioutil.ReadAll(r.Body) //http.MaxBytesReader(w, r.Body, 64*1024))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// if the request body is form-encoded, replace any
	// credential placeholders with the real credentials
	var upstreamBody io.Reader
	if strings.Contains(r.Header.Get("Content-Type"), "x-www-form-urlencoded") {
		bodyForm, err := url.ParseQuery(string(reqBodyBytes))
		if err != nil {
			http.Error(w, "error parsing request body", http.StatusBadRequest)
			return
		}
		replaceCredentials(bodyForm, oauth2Config)
		upstreamBody = strings.NewReader(bodyForm.Encode())
	}

	// now do the same thing for the query string
	qs := r.URL.Query()
	replaceCredentials(qs, oauth2Config)

	// make outgoing URL
	upstreamURL, err := url.Parse(upstreamEndpoint)
	if err != nil {
		http.Error(w, "bad upstream URL", http.StatusInternalServerError)
		return
	}
	upstreamURL.RawQuery = qs.Encode()

	// set the real credentials -- this has to be done
	// carefully because apparently a lot of OAuth2
	// providers are broken (against RFC 6749), so
	// the downstream OAuth2 client lib must be sure
	// to set the credentials in the right place, and
	// we should be sure to mirror that behavior;
	// this means that even though the downstream may
	// not have the real client ID and secret, they
	// need to provide SOMETHING as bogus placeholder
	// values to signal to us where to put the real
	// credentials
	if r.Header.Get("Authorization") != "" {
		r.SetBasicAuth(oauth2Config.ClientID, oauth2Config.ClientSecret)
	}

	// prepare the request to upstream
	upstream, err := http.NewRequest(r.Method, upstreamURL.String(), upstreamBody)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	upstream.Header = r.Header
	delete(upstream.Header, "Content-Length")

	// perform the upstream request
	resp, err := http.DefaultClient.Do(upstream)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// copy the upstream headers to the response downstream
	for key, vals := range resp.Header {
		for _, val := range vals {
			w.Header().Add(key, val)
		}
	}

	// carry over the status code
	w.WriteHeader(resp.StatusCode)

	// copy the response body downstream
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		http.Error(w, "writing body: "+err.Error(), http.StatusBadGateway)
		return
	}
}

func replaceCredentials(form url.Values, oauth2Config oauth2.Config) {
	if form.Get("client_id") != "" {
		form.Set("client_id", oauth2Config.ClientID)
	}
	if form.Get("client_secret") != "" {
		form.Set("client_secret", oauth2Config.ClientSecret)
	}
}
