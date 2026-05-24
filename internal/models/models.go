package models

import (
	"time"

	"github.com/google/uuid"
)

type User struct {
	ID           uuid.UUID  `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Email        *string    `gorm:"uniqueIndex" json:"email"`
	PasswordHash *string    `json:"-"`
	SteamID      *string    `gorm:"uniqueIndex" json:"steam_id"`
	Username     string     `gorm:"not null" json:"username"`
	AvatarURL    string     `json:"avatar_url"`
	Bio          string     `json:"bio"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type Game struct {
	ID          uuid.UUID  `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	IGDBID      *int       `gorm:"uniqueIndex" json:"igdb_id"`
	SteamAppID  int        `json:"steam_app_id"` // Used to map imported games
	Title       string     `gorm:"not null" json:"title"`
	CoverURL    string     `json:"cover_url"`
	ReleaseDate *time.Time `json:"release_date"`
	Summary     string     `json:"summary"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type UserGame struct {
	UserID          uuid.UUID  `gorm:"type:uuid;primaryKey" json:"user_id"`
	GameID          uuid.UUID  `gorm:"type:uuid;primaryKey" json:"game_id"`
	Status          string     `gorm:"not null;default:'backlog'" json:"status"` // playing, completed, backlog, dropped, wishlist
	PlaytimeMinutes int        `gorm:"default:0" json:"playtime_minutes"`
	LastPlayedAt    *time.Time `json:"last_played_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`

	User User `gorm:"foreignKey:UserID" json:"-"`
	Game Game `gorm:"foreignKey:GameID" json:"game"`
}

type Review struct {
	ID              uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	UserID          uuid.UUID `gorm:"type:uuid;not null;index" json:"user_id"`
	GameID          uuid.UUID `gorm:"type:uuid;not null;index" json:"game_id"`
	Rating          float32   `gorm:"check:rating >= 0 AND rating <= 5" json:"rating"` // 0-5 stars
	Content         string    `json:"content"`
	ContainsSpoiler bool      `gorm:"default:false" json:"contains_spoiler"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`

	User User `gorm:"foreignKey:UserID" json:"user"`
	Game Game `gorm:"foreignKey:GameID" json:"game"`
}

type Follow struct {
	FollowerID  uuid.UUID `gorm:"type:uuid;primaryKey" json:"follower_id"`
	FollowingID uuid.UUID `gorm:"type:uuid;primaryKey" json:"following_id"`
	CreatedAt   time.Time `json:"created_at"`

	Follower  User `gorm:"foreignKey:FollowerID" json:"follower"`
	Following User `gorm:"foreignKey:FollowingID" json:"following"`
}

type Activity struct {
	ID         uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	UserID     uuid.UUID `gorm:"type:uuid;not null;index" json:"user_id"`
	ActionType string    `gorm:"not null" json:"action_type"` // rated, reviewed, status_change
	TargetID   uuid.UUID `gorm:"type:uuid;not null" json:"target_id"`
	TargetType string    `gorm:"not null" json:"target_type"` // review, user_game
	CreatedAt  time.Time `json:"created_at"`

	User User `gorm:"foreignKey:UserID" json:"user"`
}
