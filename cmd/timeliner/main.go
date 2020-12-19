package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/mholt/timeliner"
	"github.com/mholt/timeliner/oauth2client"
	"golang.org/x/oauth2"

	// plug in data sources
	_ "github.com/mholt/timeliner/datasources/facebook"
	_ "github.com/mholt/timeliner/datasources/gmail"
	_ "github.com/mholt/timeliner/datasources/googlelocation"
	_ "github.com/mholt/timeliner/datasources/googlephotos"
	_ "github.com/mholt/timeliner/datasources/instagram"
	"github.com/mholt/timeliner/datasources/smsbackuprestore"
	"github.com/mholt/timeliner/datasources/twitter"
)

func init() {
	flag.StringVar(&configFile, "config", configFile, "The path to the config file to load")
	flag.StringVar(&repoDir, "repo", repoDir, "The path to the folder of the repository")
	flag.IntVar(&maxRetries, "max-retries", maxRetries, "If > 0, will retry on failure at most this many times")
	flag.DurationVar(&retryAfter, "retry-after", retryAfter, "If > 0, will wait this long between retries")

	flag.BoolVar(&prune, "prune", prune, "When finishing, delete items not found on remote (download-all or import only)")
	flag.BoolVar(&integrity, "integrity", integrity, "Perform integrity check on existing items and reprocess if needed (download-all or import only)")
	flag.BoolVar(&reprocess, "reprocess", reprocess, "Reprocess every item that has not been modified locally (download-all or import only)")
	flag.BoolVar(&softMerge, "softmerge", softMerge, "Merge incoming data with existing row using 'soft' keys (account ID + item timestamp + one of text, filename, and hash)")
	flag.StringVar(&keep, "keep", keep, "Comma-separated list of existing values to keep if merge is performed (preferring existing value): id,ts,text,file")

	flag.StringVar(&tfStartInput, "start", "", "Timeframe start (relative=duration, absolute=YYYY/MM/DD)")
	flag.StringVar(&tfEndInput, "end", "", "Timeframe end (relative=duration, absolute=YYYY/MM/DD)")

	flag.BoolVar(&twitterRetweets, "twitter-retweets", twitterRetweets, "Twitter: include retweets")
	flag.BoolVar(&twitterReplies, "twitter-replies", twitterReplies, "Twitter: include replies that are not just replies to self")

	flag.StringVar(&phoneDefaultRegion, "phone-default-region", phoneDefaultRegion, "SMS Backup & Restore: default region")
}

func main() {
	flag.Parse()

	if maxRetries < 0 {
		maxRetries = 0
	}

	// split the CLI arguments into subcommand and arguments
	args := flag.Args()
	if len(args) == 0 {
		log.Fatal("[FATAL] Missing subcommand and account arguments (specify one or more of 'data_source_id/user_id')")
	}
	subcmd := args[0]
	accountList := args[1:]
	if subcmd == "import" {
		// special case; import takes an extra argument before account list
		if len(args) != 3 {
			log.Fatal("[FATAL] Expecting: import <filename> <data_source_id/user_id>")
		}
		accountList = args[2:]
		if len(args) == 0 {
			log.Fatal("[FATAL] No accounts to use (specify one or more 'data_source_id/user_id' arguments)")
		}
	}

	// load the command config
	err := loadConfig()
	if err != nil {
		log.Fatalf("[FATAL] Loading configuration: %v", err)
	}

	// parse the accounts out of the CLI
	accounts, err := getAccounts(accountList)
	if err != nil {
		log.Fatalf("[FATAL] %v", err)
	}
	if len(accounts) == 0 {
		log.Fatalf("[FATAL] No accounts specified")
	}

	// open the timeline
	tl, err := timeliner.Open(repoDir)
	if err != nil {
		log.Fatalf("[FATAL] Opening timeline: %v", err)
	}
	defer tl.Close()

	// as a special case, handle authentication subcommands separately
	switch subcmd {
	case "add-account":
		for _, a := range accounts {
			err := tl.AddAccount(a.dataSourceID, a.userID)
			if err != nil {
				log.Fatalf("[FATAL] Adding account: %v", err)
			}
		}
		return
	case "reauth":
		for _, a := range accounts {
			err := tl.Authenticate(a.dataSourceID, a.userID)
			if err != nil {
				log.Fatalf("[FATAL] Authenticating: %v", err)
			}
		}
		return
	}

	// get the timeframe within which to constrain item processing (multiple commands use this)
	tf, err := parseTimeframe()
	if err != nil {
		log.Fatalf("[FATAL] %v", err)
	}

	// make the processing options
	mergeOptions := timeliner.MergeOptions{SoftMerge: softMerge}
	keepFields := strings.Split(keep, ",")
	for _, val := range keepFields {
		switch val {
		case "":
		case "id":
			mergeOptions.PreferExistingID = true
		case "ts":
			mergeOptions.PreferExistingTimestamp = true
		case "text":
			mergeOptions.PreferExistingDataText = true
		case "file":
			mergeOptions.PreferExistingDataFile = true
		default:
			log.Fatalf("[FATAL] Unrecognized value for 'keep' argument: '%s'", val)
		}
	}
	procOpt := timeliner.ProcessingOptions{
		Reprocess: reprocess,
		Prune:     prune,
		Integrity: integrity,
		Timeframe: tf,
		Merge:     mergeOptions,
	}

	// make a client for each account
	var clients []timeliner.WrappedClient
	for _, a := range accounts {
		wc, err := tl.NewClient(a.dataSourceID, a.userID)
		if err != nil {
			log.Fatalf("[FATAL][%s/%s] Creating data source client: %v", a.dataSourceID, a.userID, err)
		}

		// configure the client
		switch v := wc.Client.(type) {
		case *twitter.Client:
			v.Retweets = twitterRetweets
			v.Replies = twitterReplies
		case *smsbackuprestore.Client:
			v.DefaultRegion = phoneDefaultRegion
		}

		clients = append(clients, wc)
	}

	switch subcmd {
	case "get-latest":
		if procOpt.Reprocess || procOpt.Prune || procOpt.Integrity || procOpt.Timeframe.Since != nil {
			log.Fatalf("[FATAL] The get-latest subcommand does not support -reprocess, -prune, -integrity, or -start")
		}

		var wg sync.WaitGroup
		for _, wc := range clients {
			wg.Add(1)
			go func(wc timeliner.WrappedClient) {
				defer wg.Done()
				ctx, cancel := context.WithCancel(context.Background())
				for retryNum := 0; retryNum < 1+maxRetries; retryNum++ {
					if retryNum > 0 {
						log.Println("[INFO] Retrying command")
					}
					err := wc.GetLatest(ctx, tf.Until)
					if err != nil {
						log.Printf("[ERROR][%s/%s] Getting latest: %v",
							wc.DataSourceID(), wc.UserID(), err)
						if retryAfter > 0 {
							time.Sleep(retryAfter)
						}
						continue
					}
					break
				}
				defer cancel() // TODO: Make this useful, maybe?
			}(wc)
		}
		wg.Wait()

	case "get-all":
		var wg sync.WaitGroup
		for _, wc := range clients {
			wg.Add(1)
			go func(wc timeliner.WrappedClient) {
				defer wg.Done()
				ctx, cancel := context.WithCancel(context.Background())
				for retryNum := 0; retryNum < 1+maxRetries; retryNum++ {
					if retryNum > 0 {
						log.Println("[INFO] Retrying command")
					}
					err := wc.GetAll(ctx, procOpt)
					if err != nil {
						log.Printf("[ERROR][%s/%s] Downloading all: %v",
							wc.DataSourceID(), wc.UserID(), err)
						if retryAfter > 0 {
							time.Sleep(retryAfter)
						}
						continue
					}
					break
				}
				defer cancel() // TODO: Make this useful, maybe?
			}(wc)
		}
		wg.Wait()

	case "import":
		file := args[1]
		wc := clients[0]

		ctx, cancel := context.WithCancel(context.Background())
		err = wc.Import(ctx, file, procOpt)
		if err != nil {
			log.Printf("[ERROR][%s/%s] Importing: %v",
				wc.DataSourceID(), wc.UserID(), err)
		}
		defer cancel() // TODO: Make this useful, maybe?

	default:
		log.Fatalf("[FATAL] Unrecognized subcommand: %s", subcmd)
	}
}

// parseTimeframe parses tfStartInput and/or tfEndInput and returns
// the resulting timeframe or an error.
func parseTimeframe() (timeliner.Timeframe, error) {
	var tf timeliner.Timeframe
	var timeStart, timeEnd time.Time

	if tfStartInput != "" {
		tfStartRel, err := time.ParseDuration(tfStartInput)
		if err == nil {
			timeStart = time.Now().Add(tfStartRel)
		} else {
			timeStart, err = time.Parse(dateFormat, tfStartInput)
			if err != nil {
				return tf, fmt.Errorf("bad timeframe start value '%s': %v", tfStartInput, err)
			}
		}
		tf.Since = &timeStart
	}

	if tfEndInput != "" {
		tfEndRel, err := time.ParseDuration(tfEndInput)
		if err == nil {
			timeEnd = time.Now().Add(tfEndRel)
		} else {
			timeEnd, err = time.Parse(dateFormat, tfEndInput)
			if err != nil {
				return tf, fmt.Errorf("bad timeframe end value '%s': %v", tfEndInput, err)
			}
		}
		tf.Until = &timeEnd
	}

	if tf.Since != nil && tf.Until != nil && tf.Until.Before(*tf.Since) {
		return tf, fmt.Errorf("end time must be after start time (start=%s end=%s)", tf.Since, tf.Until)
	}

	return tf, nil
}

func loadConfig() error {
	// no config file is allowed, but that might be useless
	_, err := os.Stat(configFile)
	if os.IsNotExist(err) {
		return nil
	}

	var cmdConfig commandConfig
	md, err := toml.DecodeFile(configFile, &cmdConfig)
	if err != nil {
		return fmt.Errorf("decoding config file: %v", err)
	}
	if len(md.Undecoded()) > 0 {
		return fmt.Errorf("unrecognized key(s) in config file: %+v", md.Undecoded())
	}

	// convert them into oauth2.Configs (the structure of
	// oauth2.Config as TOML is too verbose for my taste)
	// (important to not be pointer values, since the
	// oauth2.Configs need to be copied and changed for
	// each token source that is created)
	oauth2Configs := make(map[string]oauth2.Config)
	for id, prov := range cmdConfig.OAuth2.Providers {
		if prov.RedirectURL == "" {
			prov.RedirectURL = oauth2client.DefaultRedirectURL
		}
		oauth2Configs[id] = oauth2.Config{
			ClientID:     prov.ClientID,
			ClientSecret: prov.ClientSecret,
			RedirectURL:  prov.RedirectURL,
			Endpoint: oauth2.Endpoint{
				AuthURL:  prov.AuthURL,
				TokenURL: prov.TokenURL,
			},
		}
	}

	// TODO: Should this be passed into timeliner.Open() instead?
	timeliner.OAuth2AppSource = func(providerID string, scopes []string) (oauth2client.App, error) {
		cfg, ok := oauth2Configs[providerID]
		if !ok {
			return nil, fmt.Errorf("unsupported provider: %s", providerID)
		}
		cfg.Scopes = scopes
		return oauth2client.LocalAppSource{OAuth2Config: &cfg}, nil
	}

	return nil
}

func getAccounts(args []string) ([]accountInfo, error) {
	var accts []accountInfo
	for _, a := range args {
		parts := strings.SplitN(a, "/", 2)
		if len(parts) < 2 {
			return nil, fmt.Errorf("malformed account identifier '%s': expecting '<data_source>/<account>' format", a)
		}
		accts = append(accts, accountInfo{
			dataSourceID: parts[0],
			userID:       parts[1],
		})
	}
	return accts, nil
}

type accountInfo struct {
	dataSourceID string
	userID       string
}

type commandConfig struct {
	OAuth2 oauth2Config `toml:"oauth2"`
}

type oauth2Config struct {
	Providers map[string]oauth2ProviderConfig `toml:"providers"`
}

type oauth2ProviderConfig struct {
	ClientID     string `toml:"client_id"`
	ClientSecret string `toml:"client_secret"`
	RedirectURL  string `toml:"redirect_url"`
	AuthURL      string `toml:"auth_url"`
	TokenURL     string `toml:"token_url"`
}

var (
	repoDir    = "./timeliner_repo"
	configFile = "timeliner.toml"
	maxRetries int
	retryAfter time.Duration

	integrity bool
	prune     bool
	reprocess bool
	softMerge bool
	keep      string

	tfStartInput, tfEndInput string

	twitterRetweets bool
	twitterReplies  bool

	phoneDefaultRegion string = "US"
)

const dateFormat = "2006/01/02" // YYYY/MM/DD
