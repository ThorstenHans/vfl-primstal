# ig-scraper

A Go CLI that fetches the latest 9 posts from the VfL Primstal Instagram account and saves them as static assets for the Astro website.

## What it does

1. Calls the **Instagram Graph API** (`/me/media`) with a long-lived access token
2. Downloads the 9 most recent images (for videos, the thumbnail is downloaded)
3. Saves images to `src/images/instagram/1.jpg` ... `9.jpg` (overwriting on each run)
4. Writes post metadata (caption, permalink, timestamp) to `src/data/instagram.json`
5. Auto-refreshes the long-lived token on every run to prevent expiry

The Astro site is then rebuilt with the fresh content — either on every push (if images are committed) or directly in the CI pipeline.

---

## Authentication

The Instagram API requires a **long-lived access token**. This is a one-time setup.

### Requirements

- The VfL Primstal Instagram account must be a **Professional account** (Business or Creator).  
  Switch at: *Settings → Account → Switch to Professional Account*
- A **Facebook App** configured with the *Instagram API with Instagram Login* product

---

### Step-by-step: Getting the access token

#### 1. Create a Facebook App

1. Go to [developers.facebook.com/apps](https://developers.facebook.com/apps) → **Create App**
2. Choose **Other** → **Consumer** type
3. Name the app (e.g. *VfL Primstal Website*)

#### 2. Add Instagram Login product

1. In your app dashboard, find **Products** → click **Set up** next to **Instagram**
2. Choose **Instagram API with Instagram Login**

#### 3. Configure OAuth & permissions

1. Under **Instagram** → **API setup with Instagram login** → **Settings**
2. Add a valid OAuth redirect URI — for testing, `https://localhost` works fine
3. Under **Permissions**, make sure `instagram_business_basic` is listed

#### 4. Generate a short-lived token

Open this URL in your browser (replace `APP_ID` and `REDIRECT_URI`):

```
https://api.instagram.com/oauth/authorize
  ?client_id=APP_ID
  &redirect_uri=REDIRECT_URI
  &scope=instagram_business_basic
  &response_type=code
```

After authorizing, you will be redirected to `REDIRECT_URI?code=AUTH_CODE`. Copy the `AUTH_CODE`.

Exchange it for a short-lived token:

```bash
curl -X POST https://api.instagram.com/oauth/access_token \
  -F client_id=APP_ID \
  -F client_secret=APP_SECRET \
  -F grant_type=authorization_code \
  -F redirect_uri=REDIRECT_URI \
  -F code=AUTH_CODE
```

#### 5. Exchange for a long-lived token (60 days)

```bash
curl "https://graph.instagram.com/access_token \
  ?grant_type=ig_exchange_token \
  &client_id=APP_ID \
  &client_secret=APP_SECRET \
  &access_token=SHORT_LIVED_TOKEN"
```

Save the returned `access_token` — this is your **long-lived token**.

> **Note:** `ig-scraper` automatically refreshes this token on every run, so it never expires as long as the cron job runs at least every 55 days.

---

## Running locally

```bash
# From the project root
export INSTAGRAM_ACCESS_TOKEN="your_long_lived_token_here"

go run ./ig-scraper --output .
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--token` | `$INSTAGRAM_ACCESS_TOKEN` | Long-lived Instagram access token |
| `--output` | `.` | Path to the Astro project root |
| `--count` | `9` | Number of posts to fetch |
| `--no-refresh` | `false` | Skip token refresh (useful for testing) |
| `--token-out` | *(none)* | Write the refreshed token to this file |

### Example

```bash
# Fetch 9 posts, save everything relative to project root
go run ./ig-scraper --output . --count 9

# Dry-run without refreshing the token
go run ./ig-scraper --output . --no-refresh

# Save refreshed token for use by GitHub Actions secret rotation
go run ./ig-scraper --output . --token-out /tmp/new_token.txt
```

---

## GitHub Actions

The workflow at `.github/workflows/update-instagram.yml` runs on a **daily cron** (06:00 UTC) and:

1. Builds `ig-scraper`
2. Runs it with the stored access token
3. If the token was refreshed to a new value, updates the `INSTAGRAM_ACCESS_TOKEN` secret *(requires `GH_PAT`)*
4. Commits changed images + metadata back to the repository
5. The updated files trigger a fresh Astro build & deploy on the next pipeline run

### Required repository secrets

| Secret | Required | Description |
|--------|----------|-------------|
| `INSTAGRAM_ACCESS_TOKEN` | Always | Long-lived Instagram access token |
| `GH_PAT` | Recommended | GitHub PAT with `secrets:write` scope — enables automatic token rotation |

### Creating `GH_PAT` (for automatic token rotation)

1. Go to **GitHub → Settings → Developer Settings → Personal access tokens (classic)**
2. Generate a token with scopes: `repo`, `admin:repo_hook`  
   *(Or use a fine-grained token with **Secrets** read+write on this repository)*
3. Add it as a repository secret named `GH_PAT`

If `GH_PAT` is not configured, the workflow skips secret rotation and continues normally — you will just need to manually update `INSTAGRAM_ACCESS_TOKEN` if it changes (which happens on each token refresh call, resetting the 60-day expiry).

---

## Output files

| File | Description |
|------|-------------|
| `src/images/instagram/1.jpg` ... `9.jpg` | Downloaded post images (newest first) |
| `src/data/instagram.json` | Post metadata array (caption, permalink, timestamp, mediaType) |

### `instagram.json` format

```json
[
  {
    "index": 1,
    "filename": "1.jpg",
    "caption": "Post caption text here",
    "permalink": "https://www.instagram.com/p/abc123/",
    "timestamp": "2024-11-15T14:30:00+0000",
    "mediaType": "IMAGE"
  }
]
```

## Notes

- **Stories** are not available via this API — only regular feed posts (images, videos, carousels).
- **Videos** are saved as their thumbnail image.
- **Carousels** (`CAROUSEL_ALBUM`) use the cover/first image.
- The Instagram `media_url` is a pre-signed URL valid for a limited time — images are downloaded immediately.
