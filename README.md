# AnyManga Go API

Run with Go 1.25.3 or newer.

```sh
go test ./...
go run .
```

The API listens on `127.0.0.1:8080` locally. Set `PORT` to bind to a managed host's assigned port. `GET /health` and `GET /api/manga/health` return HTTP 204.

- `GET /search?q=one+piece&page=1` uses MangaRead's search and keeps its result order and canonical series slugs.
- `GET /mangapage?title=one-piece` returns the description, cover, and ordered chapter index.
- `GET /chapterimages?manga=one-piece&chapter=chapter-1-romance-dawn` returns page URLs and adjacent chapters. The original POST is also supported.
- `GET /featured` and `GET /popular` return catalog cards.
- `GET /info` and `POST /chapters` support the PDF download workflow. Send chapter IDs or unique display titles.

Chapter links come from the series page. The index and chapter page URLs are cached in memory for five minutes. Identical concurrent requests share one upstream fetch.

The [frontend](https://github.com/Anime-Shhh/AnyManga-frontend) uses the same Go server on Vercel at `/api/manga/*`. When running the frontend separately, its `MANGA_API_ORIGIN` can point to any HTTPS origin serving this Go API.
