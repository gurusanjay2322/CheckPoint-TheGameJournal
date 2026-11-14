package handlers

import (
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/checkpoint/server/internal/cache"
	"github.com/checkpoint/server/internal/config"
	"github.com/checkpoint/server/internal/db"
	"github.com/checkpoint/server/internal/models"
	"github.com/checkpoint/server/internal/utils"
	"github.com/checkpoint/server/internal/worker"
	"github.com/checkpoint/server/pkg/steam"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

var countryCodeRe = regexp.MustCompile(`^[A-Za-z]{2}$`)

// A manual sync can be requested at most this often per user.
const steamSyncCooldown = 5 * time.Minute

type SteamHandler struct {
	cfg         *config.Config
	steam       *steam.Client
	asynqClient *asynq.Client

	deals   *cache.TTL[string, []steam.StoreItem]
	prices  *cache.TTL[string, steam.StoreItem]
	status  *cache.TTL[string, steam.PlayerSummary]
	friends *cache.TTL[string, []string]
}

func NewSteamHandler(cfg *config.Config, steamClient *steam.Client, asynqClient *asynq.Client) *SteamHandler {
	return &SteamHandler{
		cfg:         cfg,
		steam:       steamClient,
		asynqClient: asynqClient,
		deals:       cache.NewTTL[string, []steam.StoreItem](30 * time.Minute),
		prices:      cache.NewTTL[string, steam.StoreItem](time.Hour),
		status:      cache.NewTTL[string, steam.PlayerSummary](time.Minute),
		friends:     cache.NewTTL[string, []string](10 * time.Minute),
	}
}

func (h *SteamHandler) country(c *fiber.Ctx) string {
	if cc := c.Query("cc"); countryCodeRe.MatchString(cc) {
		return strings.ToUpper(cc)
	}
	return h.cfg.SteamStoreCountry
}

func currentUser(c *fiber.Ctx) (models.User, error) {
	var user models.User
	userID, ok := c.Locals("user_id").(string)
	if !ok {
		return user, utils.Unauthorized(c, "unauthorized")
	}
	if err := db.DB.First(&user, "id = ?", userID).Error; err != nil {
		return user, utils.NotFound(c, "user not found")
	}
	return user, nil
}

// GetDeals godoc
// @Summary Steam deals
// @Description Discounted games currently featured on the Steam store, with regional prices.
// @Tags steam
// @Produce json
// @Param cc query string false "Store country (ISO 3166-1 alpha-2, default from server config)"
// @Success 200 {array} steam.StoreItem
// @Failure 502 {object} map[string]string
// @Router /steam/deals [get]
func (h *SteamHandler) GetDeals(c *fiber.Ctx) error {
	cc := h.country(c)
	if deals, ok := h.deals.Get(cc); ok {
		return c.JSON(deals)
	}
	deals, err := h.steam.GetDeals(cc)
	if err != nil {
		log.Printf("Steam deals failed for %s: %v", cc, err)
		return c.Status(fiber.StatusBadGateway).JSON(utils.ErrorResponse{Error: "could not load Steam deals", Code: "UPSTREAM_ERROR"})
	}
	if deals == nil {
		deals = []steam.StoreItem{}
	}
	h.deals.Set(cc, deals)
	return c.JSON(deals)
}

type WishlistPrice struct {
	Game  models.Game     `json:"game"`
	Store steam.StoreItem `json:"store"`
}

// GetWishlistPrices godoc
// @Summary Wishlist prices
// @Description Current Steam prices for the user's wishlist, biggest discounts first.
// @Tags steam
// @Security BearerAuth
// @Produce json
// @Param cc query string false "Store country (ISO 3166-1 alpha-2)"
// @Success 200 {array} WishlistPrice
// @Failure 401 {object} map[string]string
// @Router /steam/wishlist [get]
func (h *SteamHandler) GetWishlistPrices(c *fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok {
		return utils.Unauthorized(c, "unauthorized")
	}
	cc := h.country(c)

	var entries []models.UserGame
	if err := db.DB.Preload("Game").
		Joins("JOIN games ON games.id = user_games.game_id").
		Where("user_games.user_id = ? AND user_games.status = ? AND games.steam_app_id IS NOT NULL", userID, "wishlist").
		Find(&entries).Error; err != nil {
		return utils.InternalError(c, "failed to load wishlist")
	}

	appIDs := make([]int, len(entries))
	for i, e := range entries {
		appIDs[i] = *e.Game.SteamAppID
	}
	items, err := h.storeItems(appIDs, cc)
	if err != nil {
		log.Printf("Steam wishlist prices failed: %v", err)
		return c.Status(fiber.StatusBadGateway).JSON(utils.ErrorResponse{Error: "could not load Steam prices", Code: "UPSTREAM_ERROR"})
	}

	result := make([]WishlistPrice, 0, len(entries))
	for _, e := range entries {
		if item, ok := items[*e.Game.SteamAppID]; ok {
			result = append(result, WishlistPrice{Game: e.Game, Store: item})
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		di, dj := discount(result[i].Store), discount(result[j].Store)
		if di != dj {
			return di > dj
		}
		return result[i].Game.Title < result[j].Game.Title
	})
	return c.JSON(result)
}

func discount(item steam.StoreItem) int {
	if item.Price == nil {
		return 0
	}
	return item.Price.DiscountPercent
}

// storeItems returns store data for appIDs, using cached entries where fresh.
func (h *SteamHandler) storeItems(appIDs []int, cc string) (map[int]steam.StoreItem, error) {
	result := make(map[int]steam.StoreItem, len(appIDs))
	var missing []int
	for _, id := range appIDs {
		if item, ok := h.prices.Get(fmt.Sprintf("%s:%d", cc, id)); ok {
			result[id] = item
		} else {
			missing = append(missing, id)
		}
	}
	for start := 0; start < len(missing); start += 100 {
		fetched, err := h.steam.GetStoreItems(missing[start:min(start+100, len(missing))], cc)
		if err != nil {
			return nil, err
		}
		for id, item := range fetched {
			h.prices.Set(fmt.Sprintf("%s:%d", cc, id), item)
			result[id] = item
		}
	}
	return result, nil
}

type SteamStatus struct {
	Linked     bool   `json:"linked"`
	Online     bool   `json:"online"`
	State      string `json:"state"`
	GameName   string `json:"game_name,omitempty"`
	GameAppID  string `json:"game_app_id,omitempty"`
	ProfileURL string `json:"profile_url,omitempty"`
}

var personaStates = []string{"offline", "online", "busy", "away", "snooze", "looking to trade", "looking to play"}

// GetStatus godoc
// @Summary Steam presence
// @Description Whether a user is online on Steam and what they are playing (requires a public Steam profile).
// @Tags steam
// @Produce json
// @Param user_id path string true "User UUID"
// @Success 200 {object} SteamStatus
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /steam/status/{user_id} [get]
func (h *SteamHandler) GetStatus(c *fiber.Ctx) error {
	userID, err := uuid.Parse(c.Params("user_id"))
	if err != nil {
		return utils.BadRequest(c, "invalid user id")
	}
	var user models.User
	if err := db.DB.Select("id", "steam_id").First(&user, userID).Error; err != nil {
		return utils.NotFound(c, "user not found")
	}
	if user.SteamID == nil {
		return c.JSON(SteamStatus{Linked: false, State: "offline"})
	}

	summary, ok := h.status.Get(*user.SteamID)
	if !ok {
		summaries, err := h.steam.GetPlayerSummaries([]string{*user.SteamID})
		if err != nil || len(summaries) == 0 {
			if err != nil {
				log.Printf("Steam status failed for %s: %v", *user.SteamID, err)
			}
			return c.JSON(SteamStatus{Linked: true, State: "unknown"})
		}
		summary = summaries[0]
		h.status.Set(*user.SteamID, summary)
	}

	state := "offline"
	if summary.PersonaState >= 0 && summary.PersonaState < len(personaStates) {
		state = personaStates[summary.PersonaState]
	}
	return c.JSON(SteamStatus{
		Linked:     true,
		Online:     summary.PersonaState != 0,
		State:      state,
		GameName:   summary.GameExtraInfo,
		GameAppID:  summary.GameID,
		ProfileURL: summary.ProfileURL,
	})
}

// GetFriends godoc
// @Summary Steam friends on Checkpoint
// @Description Checkpoint users who are the current user's Steam friends (requires a public friends list).
// @Tags steam
// @Security BearerAuth
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]string
// @Router /steam/friends [get]
func (h *SteamHandler) GetFriends(c *fiber.Ctx) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	if user.SteamID == nil {
		return c.JSON(fiber.Map{"linked": false, "data": []models.User{}})
	}

	friendIDs, ok := h.friends.Get(*user.SteamID)
	if !ok {
		friendIDs, err = h.steam.GetFriendList(*user.SteamID)
		if err != nil {
			log.Printf("Steam friends failed for %s: %v", *user.SteamID, err)
			return c.Status(fiber.StatusBadGateway).JSON(utils.ErrorResponse{Error: "could not load Steam friends", Code: "UPSTREAM_ERROR"})
		}
		h.friends.Set(*user.SteamID, friendIDs)
	}

	users := []models.User{}
	if len(friendIDs) > 0 {
		if err := db.DB.Select("id", "username", "avatar_url", "bio", "created_at").
			Where("steam_id IN ?", friendIDs).Order("username").Find(&users).Error; err != nil {
			return utils.InternalError(c, "failed to load friends")
		}
	}

	var followingIDs []uuid.UUID
	db.DB.Model(&models.Follow{}).Where("follower_id = ?", user.ID).Pluck("following_id", &followingIDs)

	return c.JSON(fiber.Map{"linked": true, "data": users, "following_ids": followingIDs})
}

// SyncNow godoc
// @Summary Sync Steam now
// @Description Queue a Steam library, wishlist and achievement sync for the current user.
// @Tags steam
// @Security BearerAuth
// @Produce json
// @Success 202 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Failure 429 {object} map[string]string
// @Router /steam/sync [post]
func (h *SteamHandler) SyncNow(c *fiber.Ctx) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	if user.SteamID == nil {
		return utils.BadRequest(c, "link a Steam account first")
	}

	task, err := worker.NewSyncSteamLibraryTask(user.ID.String(), *user.SteamID)
	if err == nil {
		_, err = h.asynqClient.Enqueue(task, asynq.Unique(steamSyncCooldown))
	}
	if errors.Is(err, asynq.ErrDuplicateTask) {
		return utils.TooManyRequests(c, "a sync was started recently; try again in a few minutes")
	}
	if err != nil {
		log.Printf("Failed to enqueue steam sync for %s: %v", user.ID, err)
		return utils.InternalError(c, "failed to start sync")
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"message": "sync started"})
}
