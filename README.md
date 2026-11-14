# Checkpoint - Server API 🎮

**Checkpoint** is an ongoing, open-source backend project that serves as a personal gaming journal API. It provides endpoints for users to log their playtime, write reviews, manage their backlog, and follow their friends' gaming activities.

> ⚠️ **Status: In Active Development** 
> This backend API is currently under heavy development. The core MVP endpoints are mostly functional, but schemas and routes are subject to change.

##  Current Features (Backend API)

We have scaffolded out the core backend using **Go (Fiber)**, backed by a **PostgreSQL (Supabase)** database and a **Redis** worker queue.

### Authentication & Onboarding
* **Email & Password:** Standard JWT-based authentication.
* **Steam Login:** Users sign up or log in through Steam OpenID. The app opens `GET /api/v1/auth/steam/login` in a browser; after Steam verifies the user, the server redirects to `APP_REDIRECT_URL?token=<jwt>` (or `?error=<code>`). Logged-in users link Steam via `POST /api/v1/auth/steam/link`, which returns a sign-in `url` to open; success redirects to `APP_REDIRECT_URL?linked=true`.
* **Library Sync:** Linking a Steam account triggers an asynchronous Redis background worker that pulls in the user's entire Steam library automatically.

### Game Discovery (Powered by IGDB)
* **Explore Feeds:** Endpoints to fetch the most popular, trending, and newly released games.
* **Rich Metadata:** Asynchronous background workers automatically enrich synced Steam games with high-res cover art, summaries, and release dates from the IGDB API.
* **Search:** Full text search for any video game via IGDB.

### Social & Journaling
* **Reviews:** Users can write detailed text reviews and rate games on a 5-star decimal scale (e.g., 4.5 stars).
* **Library Management:** Mark games as *Playing, Completed, Backlog, Dropped,* or *Wishlist*.
* **Activity Feed:** Follow other users and view a chronological feed of their gaming activity (status updates, new reviews).
* **User Profiles:** View a user's avatar, bio, total games, and complete review history.

## 🛠 Tech Stack

### Core Tech
* **Language:** Go 1.22+
* **Framework:** Fiber (v2)
* **Database:** PostgreSQL (via GORM)
* **Queue / Background Jobs:** Redis (via Asynq)
* **Documentation:** Swaggo (Swagger UI)
* **External APIs:** IGDB (Twitch), Steam Web API

##  API Documentation

The backend is fully documented using Swagger. When the server is running locally, you can view the interactive documentation and test the endpoints directly from your browser:

👉 **[http://localhost:3000/swagger/index.html](http://localhost:3000/swagger/index.html)**

##  Running the Server Locally

1. Clone the repository and navigate to the `server` directory.
2. Copy `.env.example` to `.env` and fill in `DATABASE_URL`, `REDIS_URL`, `STEAM_API_KEY`, the `IGDB` credentials, and a `JWT_SECRET` of at least 32 characters (the API refuses to start otherwise). Set `PUBLIC_URL` to the API's externally reachable URL so Steam can redirect back to it.
3. Start the API server with hot-reloading:
   ```bash
   air
   ```
4. In a separate terminal, start the background worker to handle Steam syncing and IGDB enrichment:
   ```bash
   go run cmd/worker/main.go
   ```
