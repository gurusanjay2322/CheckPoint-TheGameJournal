package igdb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Client struct {
	ClientID     string
	ClientSecret string
	HTTPClient   *http.Client

	mu          sync.Mutex
	accessToken string
	tokenExpiry time.Time
}

func NewClient(clientID, clientSecret string) *Client {
	return &Client{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		HTTPClient:   &http.Client{Timeout: 15 * time.Second},
	}
}

// token returns a valid app access token, refreshing it if needed.
func (c *Client) token() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.accessToken != "" && time.Now().Before(c.tokenExpiry) {
		return c.accessToken, nil
	}

	params := url.Values{
		"client_id":     {c.ClientID},
		"client_secret": {c.ClientSecret},
		"grant_type":    {"client_credentials"},
	}
	resp, err := c.HTTPClient.Post("https://id.twitch.tv/oauth2/token?"+params.Encode(), "application/json", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to authenticate with twitch, status: %d", resp.StatusCode)
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	c.accessToken = result.AccessToken
	// Refresh a minute early so in-flight requests don't race the expiry.
	c.tokenExpiry = time.Now().Add(time.Duration(result.ExpiresIn)*time.Second - time.Minute)

	return c.accessToken, nil
}

func (c *Client) invalidateToken() {
	c.mu.Lock()
	c.accessToken = ""
	c.mu.Unlock()
}

func (c *Client) PostRequest(endpoint, query string) ([]byte, error) {
	token, err := c.token()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", "https://api.igdb.com/v4/"+endpoint, bytes.NewBufferString(query))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Client-ID", c.ClientID)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		c.invalidateToken()
	}
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("igdb api error: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	return io.ReadAll(resp.Body)
}

// QuoteString renders s as an Apicalypse string literal, escaping characters
// that would otherwise let user input break out of the query.
func QuoteString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

type IGDBGame struct {
	ID               int    `json:"id"`
	Name             string `json:"name"`
	Summary          string `json:"summary"`
	FirstReleaseDate int64  `json:"first_release_date"`
	Cover            struct {
		ImageID string `json:"image_id"`
	} `json:"cover"`
	ExternalGames []struct {
		UID      string `json:"uid"`
		Category int    `json:"category"`
	} `json:"external_games"`
}

const gameFields = "id, name, summary, first_release_date, cover.image_id, external_games.uid, external_games.category"

func (c *Client) GetGame(id int) (*IGDBGame, error) {
	games, err := c.queryGames(fmt.Sprintf(`fields %s; where id = %d; limit 1;`, gameFields, id))
	if err != nil || len(games) == 0 {
		return nil, err
	}
	return &games[0], nil
}

func (c *Client) GetGamesBySteamIDs(steamIDs []int) ([]IGDBGame, error) {
	if len(steamIDs) == 0 {
		return nil, nil
	}

	uids := make([]string, len(steamIDs))
	for i, id := range steamIDs {
		uids[i] = strconv.Quote(strconv.Itoa(id))
	}

	return c.queryGames(fmt.Sprintf(`fields %s; where external_games.uid = (%s); limit 500;`, gameFields, strings.Join(uids, ", ")))
}

func (c *Client) queryGames(query string) ([]IGDBGame, error) {
	data, err := c.PostRequest("games", query)
	if err != nil {
		return nil, err
	}

	var games []IGDBGame
	if err := json.Unmarshal(data, &games); err != nil {
		return nil, err
	}
	return games, nil
}

func (g *IGDBGame) CoverURL() string {
	if g.Cover.ImageID == "" {
		return ""
	}
	return fmt.Sprintf("https://images.igdb.com/igdb/image/upload/t_cover_big/%s.jpg", g.Cover.ImageID)
}
