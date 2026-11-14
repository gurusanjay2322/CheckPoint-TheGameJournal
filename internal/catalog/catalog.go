// Package catalog owns the games table: lookup by external IDs and merging
// rows that turn out to be the same game.
//
// A game can enter the table two ways: Steam sync (steam_app_id set, igdb_id
// unknown until enrichment) or a user action on an IGDB game (igdb_id set,
// steam_app_id unknown). When enrichment discovers both rows describe the same
// game, they are merged into the row that holds the igdb_id.
package catalog

import (
	"errors"
	"strings"
	"unicode"

	"github.com/checkpoint/server/internal/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// FindOrCreateByIGDB returns the game with igdbID, creating a placeholder row
// titled title if none exists. created reports whether a row was inserted, in
// which case the caller should enqueue enrichment.
func FindOrCreateByIGDB(tx *gorm.DB, igdbID int, title string) (game models.Game, created bool, err error) {
	game = models.Game{IGDBID: &igdbID, Title: title}
	result := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "igdb_id"}},
		DoNothing: true,
	}).Create(&game)
	if result.Error != nil {
		return models.Game{}, false, result.Error
	}
	if result.RowsAffected == 1 {
		return game, true, nil
	}

	game = models.Game{}
	err = tx.Where("igdb_id = ?", igdbID).First(&game).Error
	return game, false, err
}

// LinkSteamGameToIGDB records that steamGame (a Steam-synced row) is IGDB game
// igdbID. apply copies IGDB metadata onto whichever row survives. If another
// row already owns igdbID, steamGame is merged into it.
func LinkSteamGameToIGDB(db *gorm.DB, steamGame *models.Game, igdbID int, apply func(*models.Game)) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var existing models.Game
		err := tx.Where("igdb_id = ? AND id <> ?", igdbID, steamGame.ID).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			steamGame.IGDBID = &igdbID
			apply(steamGame)
			return tx.Save(steamGame).Error
		}
		if err != nil {
			return err
		}

		if existing.SteamAppID == nil {
			existing.SteamAppID = steamGame.SteamAppID
		}
		apply(&existing)
		if err := Merge(tx, existing.ID, steamGame.ID); err != nil {
			return err
		}
		if err := tx.Save(&existing).Error; err != nil {
			return err
		}
		// steamGame's row is gone; mark it linked so callers skip it rather
		// than Save it (gorm's Save re-inserts when the UPDATE matches nothing).
		steamGame.IGDBID = &igdbID
		return nil
	})
}

// Merge moves everything that references game drop onto game keep and
// deletes drop. Must run inside a transaction.
//
// Where a user has a library entry for both, keep's status wins and the
// larger playtime is kept. Where a user reviewed both, keep's review wins.
func Merge(tx *gorm.DB, keep, drop uuid.UUID) error {
	statements := []struct {
		sql  string
		args []interface{}
	}{
		{`UPDATE user_games k SET playtime_minutes = GREATEST(k.playtime_minutes, d.playtime_minutes)
		  FROM user_games d WHERE k.game_id = ? AND d.game_id = ? AND d.user_id = k.user_id`, []interface{}{keep, drop}},
		{`UPDATE user_games d SET game_id = ? WHERE d.game_id = ?
		  AND NOT EXISTS (SELECT 1 FROM user_games k WHERE k.user_id = d.user_id AND k.game_id = ?)`, []interface{}{keep, drop, keep}},
		{`DELETE FROM user_games WHERE game_id = ?`, []interface{}{drop}},

		{`UPDATE reviews d SET game_id = ? WHERE d.game_id = ?
		  AND NOT EXISTS (SELECT 1 FROM reviews k WHERE k.user_id = d.user_id AND k.game_id = ?)`, []interface{}{keep, drop, keep}},
		{`DELETE FROM activities WHERE target_type = 'review' AND target_id IN (SELECT id FROM reviews WHERE game_id = ?)`, []interface{}{drop}},
		{`DELETE FROM reviews WHERE game_id = ?`, []interface{}{drop}},

		{`UPDATE activities SET target_id = ? WHERE target_type = 'game' AND target_id = ?`, []interface{}{keep, drop}},
		{`DELETE FROM games WHERE id = ?`, []interface{}{drop}},
	}

	for _, s := range statements {
		if err := tx.Exec(s.sql, s.args...).Error; err != nil {
			return err
		}
	}
	return nil
}

// TitlesMatch reports whether two titles plausibly name the same game. Used
// to reject IGDB external-ID collisions (e.g. a Twitch UID equal to a Steam
// app ID).
func TitlesMatch(a, b string) bool {
	na, nb := normalizeTitle(a), normalizeTitle(b)
	if na == "" || nb == "" {
		return false
	}
	return strings.Contains(na, nb) || strings.Contains(nb, na)
}

func normalizeTitle(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(unicode.ToLower(r))
		}
	}
	return sb.String()
}
