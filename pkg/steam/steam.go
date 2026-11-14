package steam

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	APIKey     string
	HTTPClient *http.Client
}

func NewClient(apiKey string) *Client {
	return &Client{
		APIKey:     apiKey,
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// StatusError is returned for non-200 responses. Steam answers 401/403 for
// private profiles and 400 for games without stats.
type StatusError struct{ Code int }

func (e *StatusError) Error() string { return fmt.Sprintf("steam api error: %d", e.Code) }

func isStatus(err error, codes ...int) bool {
	var se *StatusError
	if !errors.As(err, &se) {
		return false
	}
	for _, c := range codes {
		if se.Code == c {
			return true
		}
	}
	return false
}

type PlayerSummary struct {
	SteamID     string `json:"steamid"`
	PersonaName string `json:"personaname"`
	AvatarFull  string `json:"avatarfull"`
	ProfileURL  string `json:"profileurl"`
	// 0 offline, 1 online, 2 busy, 3 away, 4 snooze, 5 looking to trade, 6 looking to play.
	PersonaState int `json:"personastate"`
	// Set only while the user is in a game and their profile is public.
	GameID        string `json:"gameid"`
	GameExtraInfo string `json:"gameextrainfo"`
}

type OwnedGame struct {
	AppID           int    `json:"appid"`
	Name            string `json:"name"`
	PlaytimeForever int    `json:"playtime_forever"` // in minutes
	Playtime2Weeks  int    `json:"playtime_2weeks"`  // in minutes
	RTimeLastPlayed int64  `json:"rtime_last_played"`
}

type WishlistItem struct {
	AppID     int   `json:"appid"`
	Priority  int   `json:"priority"`
	DateAdded int64 `json:"date_added"`
}

func (c *Client) GetPlayerSummaries(steamIDs []string) ([]PlayerSummary, error) {
	if len(steamIDs) == 0 {
		return nil, nil
	}

	params := url.Values{
		"key":      {c.APIKey},
		"steamids": {strings.Join(steamIDs, ",")},
	}

	var result struct {
		Response struct {
			Players []PlayerSummary `json:"players"`
		} `json:"response"`
	}
	if err := c.getJSON("https://api.steampowered.com/ISteamUser/GetPlayerSummaries/v0002/", params, &result); err != nil {
		return nil, err
	}

	return result.Response.Players, nil
}

// GetOwnedGames returns an empty list (not an error) for private profiles.
func (c *Client) GetOwnedGames(steamID string) ([]OwnedGame, error) {
	params := url.Values{
		"key":                       {c.APIKey},
		"steamid":                   {steamID},
		"format":                    {"json"},
		"include_appinfo":           {"1"},
		"include_played_free_games": {"1"},
	}

	var result struct {
		Response struct {
			Games []OwnedGame `json:"games"`
		} `json:"response"`
	}
	if err := c.getJSON("https://api.steampowered.com/IPlayerService/GetOwnedGames/v0001/", params, &result); err != nil {
		return nil, err
	}

	return result.Response.Games, nil
}

// GetFriendList returns friends' SteamIDs; empty for private friend lists.
func (c *Client) GetFriendList(steamID string) ([]string, error) {
	params := url.Values{
		"key":          {c.APIKey},
		"steamid":      {steamID},
		"relationship": {"friend"},
	}

	var result struct {
		FriendsList struct {
			Friends []struct {
				SteamID string `json:"steamid"`
			} `json:"friends"`
		} `json:"friendslist"`
	}
	err := c.getJSON("https://api.steampowered.com/ISteamUser/GetFriendList/v0001/", params, &result)
	if isStatus(err, http.StatusUnauthorized, http.StatusForbidden) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	ids := make([]string, len(result.FriendsList.Friends))
	for i, f := range result.FriendsList.Friends {
		ids[i] = f.SteamID
	}
	return ids, nil
}

// GetPlayerAchievements returns unlocked/total counts. ok is false when the
// game has no achievements or the user's game details are private.
func (c *Client) GetPlayerAchievements(steamID string, appID int) (unlocked, total int, ok bool, err error) {
	params := url.Values{
		"key":     {c.APIKey},
		"steamid": {steamID},
		"appid":   {strconv.Itoa(appID)},
	}

	var result struct {
		PlayerStats struct {
			Success      bool `json:"success"`
			Achievements []struct {
				Achieved int `json:"achieved"`
			} `json:"achievements"`
		} `json:"playerstats"`
	}
	err = c.getJSON("https://api.steampowered.com/ISteamUserStats/GetPlayerAchievements/v0001/", params, &result)
	if isStatus(err, http.StatusBadRequest, http.StatusForbidden, http.StatusUnauthorized) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	if !result.PlayerStats.Success || len(result.PlayerStats.Achievements) == 0 {
		return 0, 0, false, nil
	}

	for _, a := range result.PlayerStats.Achievements {
		if a.Achieved == 1 {
			unlocked++
		}
	}
	return unlocked, len(result.PlayerStats.Achievements), true, nil
}

// GetWishlist needs no API key; it is empty when the wishlist is private.
func (c *Client) GetWishlist(steamID string) ([]WishlistItem, error) {
	var result struct {
		Response struct {
			Items []WishlistItem `json:"items"`
		} `json:"response"`
	}
	err := c.getJSON("https://api.steampowered.com/IWishlistService/GetWishlist/v1/", url.Values{"steamid": {steamID}}, &result)
	if isStatus(err, http.StatusUnauthorized, http.StatusForbidden) {
		return nil, nil
	}
	return result.Response.Items, err
}

func (c *Client) getJSON(endpoint string, params url.Values, out interface{}) error {
	resp, err := c.HTTPClient.Get(endpoint + "?" + params.Encode())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &StatusError{Code: resp.StatusCode}
	}

	return json.NewDecoder(resp.Body).Decode(out)
}
