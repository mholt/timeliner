package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/BurntSushi/toml"
	"github.com/mholt/timeliner/oauth2client/oauth2proxy"
	"golang.org/x/oauth2"
)

func init() {
	flag.StringVar(&credentialsFile, "credentials", credentialsFile, "The path to the file containing the OAuth2 app credentials for each provider")
	flag.StringVar(&addr, "addr", addr, "The address to listen on")
	flag.StringVar(&basePath, "path", basePath, "The base path on which to serve the proxy endpoints")
}

var (
	credentialsFile = "credentials.toml"
	addr            = ":7233"
	basePath        = "/oauth2"
)

func main() {
	flag.Parse()

	if credentialsFile == "" {
		log.Fatal("[FATAL] No credentials file specified (use -credentials)")
	}
	if addr == "" {
		log.Fatal("[FATAL] No address specified (use -addr)")
	}

	// decode app credentials
	var creds oauth2Credentials
	md, err := toml.DecodeFile(credentialsFile, &creds)
	if err != nil {
		log.Fatalf("[FATAL] Decoding credentials file: %v", err)
	}
	if len(md.Undecoded()) > 0 {
		log.Fatalf("[FATAL] Unrecognized key(s) in credentials file: %+v", md.Undecoded())
	}

	// convert them into oauth2.Configs (the structure of
	// oauth2.Config as TOML is too verbose for my taste)
	oauth2Configs := make(map[string]oauth2.Config)
	for id, prov := range creds.Providers {
		oauth2Configs[id] = oauth2.Config{
			ClientID:     prov.ClientID,
			ClientSecret: prov.ClientSecret,
			Endpoint: oauth2.Endpoint{
				AuthURL:  prov.AuthURL,
				TokenURL: prov.TokenURL,
			},
		}
		log.Println("Provider:", id)
	}

	log.Println("Serving OAuth2 proxy on", addr)

	p := oauth2proxy.New(basePath, oauth2Configs)
	http.ListenAndServe(addr, p)
}

type oauth2Credentials struct {
	Providers map[string]oauth2ProviderConfig `toml:"providers"`
}

type oauth2ProviderConfig struct {
	ClientID     string `toml:"client_id"`
	ClientSecret string `toml:"client_secret"`
	AuthURL      string `toml:"auth_url"`
	TokenURL     string `toml:"token_url"`
}
