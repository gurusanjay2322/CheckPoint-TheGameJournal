package steam

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	APIKey string
	HTTPClient *http.Client
}

func NewClient(apiKey string) *Client {
	return &Client{
		APIKey: apiKey,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

type PlayerSummary struct {
	SteamID    string `json:"steamid"`
	PersonaName string `json:"personaname"`
	AvatarFull string `json:"avatarfull"`
}

type OwnedGame struct {
	AppID           int    `json:"appid"`
	Name            string `json:"name"`
	PlaytimeForever int    `json:"playtime_forever"` // in minutes
}

func (c *Client) GetPlayerSummaries(steamIDs []string) ([]PlayerSummary, error) {
	if len(steamIDs) == 0 {
		return nil, nil
	}

	steamIDList := ""
	for i, id := range steamIDs {
		if i > 0 {
			steamIDList += ","
		}
		steamIDList += id
	}

	url := fmt.Sprintf("http://api.steampowered.com/ISteamUser/GetPlayerSummaries/v0002/?key=%s&steamids=%s", c.APIKey, steamIDList)
	resp, err := c.HTTPClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("steam api error: %d", resp.StatusCode)
	}

	var result struct {
		Response struct {
			Players []PlayerSummary `json:"players"`
		} `json:"response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Response.Players, nil
}

func (c *Client) GetOwnedGames(steamID string) ([]OwnedGame, error) {
	url := fmt.Sprintf("http://api.steampowered.com/IPlayerService/GetOwnedGames/v0001/?key=%s&steamid=%s&format=json&include_appinfo=1&include_played_free_games=1", c.APIKey, steamID)
	resp, err := c.HTTPClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("steam api error: %d", resp.StatusCode)
	}

	var result struct {
		Response struct {
			Games []OwnedGame `json:"games"`
		} `json:"response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Response.Games, nil
}
