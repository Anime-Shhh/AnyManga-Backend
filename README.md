# AnyManga Go API

Run with Go 1.25.3 or newer:

```sh
go test -race ./...
go run .
```

The API listens on `127.0.0.1:8080` by default. `PORT` binds a managed host's assigned port; `BIND_ADDR` overrides it for an explicit address such as `127.0.0.1:8080`. All endpoints also exist under `/api/manga`. Health returns HTTP 204.

## Providers

- Metadata and search: MangaDex → MangaRead → WeebCentral → MangaPill.
- English Japanese manga chapters: WeebCentral → MangaPill → MangaRead → MangaDex hosted pages.
- Manhwa/manhua chapters: WeebCentral → MangaRead → MangaDex → MangaPill.
- MangaDex publisher releases retain their external reading URL when hosted alternatives are unavailable.

MangaDex uses its documented JSON API. The other three sources are HTML adapters. Exact normalized titles and MangaDex aliases match series across sources; ambiguous matches are skipped. Numeric chapters merge in chronological order, including decimals. Specials retain their original IDs and repeated chapter numbers in different volumes remain separate. Adapters can break if upstream sites change their markup or availability.

## Endpoints

- `GET /search?q=one+piece&page=1` searches in metadata priority order. Results retain a provider-qualified series reference (`md_`, `wc_`, `mp_`); existing MangaRead slugs remain valid.
- `GET /mangapage?title=one-piece&language=en` returns preferred metadata, matched sources, available languages and the merged chapter index.
- `GET /chapterimages?manga=one-piece&chapter=chapter-1` returns page URLs, selected provider, scanlation groups, previous/next entries and any external publisher URL. Legacy POST bodies remain supported. `skip=weebcentral,mangapill` excludes failed image sources when the reader requests another source.
- `GET /featured`, `/popular`, `/category?name=manhwa` preserve the curated MangaRead rows, with MangaDex catalog fallback on upstream failure. Category also accepts `manhua`.
- `GET /image?url=...` proxies allowlisted provider images with bounded downloads, public-DNS checks and browser cache headers. Non-image responses and private destinations are rejected.
- `GET /info` and `POST /chapters` support PDF downloads. Send a series reference and one to ten chapter IDs. External publisher releases cannot be downloaded through this API. Each PDF is limited to 200 pages and 64 MiB of source images, with two concurrent PDF jobs.

Series, search and chapter-content caches are bounded and deduplicate concurrent requests. MangaDex manifests expire from the cache after three minutes; cached browser chapter metadata lasts two minutes. The server warms the next chapter's page list with at most two background jobs. It does not recursively prefetch an entire series. MangaDex calls have shared rate pacing, manifest pacing and a Retry-After cooldown. CDN image load reports are sent asynchronously when using MangaDex network nodes.

The frontend fetches next metadata while reading and warms the next chapter's first three images only when the final three current pages approach within 800px of the viewport. Save-Data and 2G connections skip speculative client work. Next/Previous reuse cached metadata, and provider image responses are browser-cacheable.

Offline tests: `go test -race ./...`. Optional live provider/image smoke test: `LIVE_PROVIDERS=1 go test ./scrapers -run TestLiveProviderPages -count=1 -v`.

See the [frontend self-hosting guide](https://github.com/Anime-Shhh/AnyManga-frontend/blob/main/SELF_HOSTING.md) for connecting a PC through Cloudflare Tunnel. Keep the Go service on loopback; do not forward your router's API port.

## Streaming PDF downloads

`POST /chapters` now emits a PDF header and each JPEG/PNG page as it is processed, flushing before fetching the next image. It retains the two-job, ten-chapter, 200-page and 64 MiB source-image limits, adds a 128 MiB PDF output limit, and bounds the operation to 290 seconds. Metadata/chapter-list validation precedes streaming; upstream images are still downloaded one at a time with the existing restricted image client. PNG images are losslessly compressed as RGB on a white background; JPEG bytes remain embedded directly. Only the current image and small PDF cross-reference/page tables are retained, rather than an accumulated document.

An interrupted stream deliberately has no final PDF trailer. Browser consumers must check the PDF signature and final `%%EOF` before saving; HTTP 200 alone cannot prove completion once headers are sent. Cancellation propagates to image requests. Frontend progress shows received bytes, current transfer speed, elapsed time, and Cancel; the final size is unknown until generation finishes, so no percentage or ETA is fabricated.

Offline streaming regressions block the final upstream image and assert that real HTTP clients already receive first-page bytes, validate final object offsets, reject incomplete/error paths, and check source budgets. `PDF_TEST_OUTPUT=/path/sample.pdf go test ./scrapers -run TestStreamingPDFStructureAndPartialWrites` writes a JPEG/PNG fixture for independent PDF-reader validation.

Series and chapter responses now include optional canonical chapter `number` strings for fixed numeric range selection. `chapterimages?prefetch=0` retains the same response contract but skips server-side next-chapter warming for first-page hover previews; ordinary requests preserve warming. The existing content cache, delivery restrictions, provider priorities and API limits still apply.
