package igdb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	ClientID     string
	ClientSecret string
	AccessToken  string
	TokenExpiry  time.Time
}

func NewClient(clientID, clientSecret string) *Client {
	return &Client{
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}
}

func (c *Client) Authenticate() error {
	if c.AccessToken != "" && time.Now().Before(c.TokenExpiry) {
		return nil
	}

	url := fmt.Sprintf("https://id.twitch.tv/oauth2/token?client_id=%s&client_secret=%s&grant_type=client_credentials", c.ClientID, c.ClientSecret)
	
	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to authenticate with twitch, status: %d", resp.StatusCode)
	}

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	c.AccessToken = result.AccessToken
	c.TokenExpiry = time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)

	return nil
}

func (c *Client) PostRequest(endpoint, query string) ([]byte, error) {
	if err := c.Authenticate(); err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://api.igdb.com/v4/%s", endpoint)
	req, err := http.NewRequest("POST", url, bytes.NewBufferString(query))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Client-ID", c.ClientID)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.AccessToken))
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("igdb api error: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	return io.ReadAll(resp.Body)
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

func (c *Client) GetGamesBySteamIDs(steamIDs []int) ([]IGDBGame, error) {
	if len(steamIDs) == 0 {
		return nil, nil
	}

	// Format steamIDs as ("id1", "id2", ...)
	var uidStr string
	for i, id := range steamIDs {
		if i > 0 {
			uidStr += ", "
		}
		uidStr += fmt.Sprintf(`"%d"`, id)
	}

	query := fmt.Sprintf(`
		fields id, name, summary, first_release_date, cover.image_id, external_games.uid, external_games.category;
		where external_games.uid = (%s);
		limit 500;
	`, uidStr)

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
