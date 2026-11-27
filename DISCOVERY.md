# Provider discovery feeds

`GET /discovery` is an authenticated metadata endpoint used by the existing
Next.js server-only forwarder. It leaves the reading fallback order, chapter
indexes, image delivery, PDFs, and existing category endpoints unchanged.

## Sources inspected and tested (2026-11-27)

| Provider | Popular signal | Recent releases | Newly added catalog entries | Numeric analytics |
| --- | --- | --- | --- | --- |
| MangaDex | `/manga?order[followedCount]=desc` | English `/chapter?order[readableAt]=desc`, both hosted and external publisher modes; batch manga lookup | `/manga?order[createdAt]=desc` | Batch `/statistics/manga`: followers and Bayesian rating out of 10 |
| WeebCentral | `/hot-series?sort=weekly_views`, ordered series links | Homepage Latest Updates, chapter labels and machine-readable times | Homepage Recently Added | No explicit view counts in the tested ranking response |
| MangaPill | Homepage Trending Mangas | `/chapters`, chapter labels and machine-readable times | `/mangas/new` | No comparable numeric traffic metrics in these feeds |
| MangaRead | Homepage popular sidebar | Homepage latest updates, chapter labels and relative times | `/manga/?m_orderby=new-manga` | No defined popularity time window or comparable view counts used |

All twelve provider/feed combinations returned usable cards in the opt-in live
check. Counts at that check (popular / recent / newly added, capped at 24):
MangaDex **24 / 24 / 24**, WeebCentral **10 / 24 / 10**, MangaPill **10 / 24 / 24**,
MangaRead **15 / 24 / 12**. These are feed samples, not catalog sizes.
MangaDex's first popular result exposed 321,098 followers and a Bayesian rating
of approximately 9.323 at that instant; those values are fetched, not hardcoded.

WeebCentral's ordinary ranking request initially returned 403 with a generic
client, then returned 200 with the application's existing User-Agent and Referer,
including from the PC network. No challenge bypass is implemented. If the
ranking is blocked or its HTML changes, the public Hot Updates list is used
with the honest **Hot updates** label, rather than **Weekly popularity**.
MangaPill's current site notice says manhwa releases have been discontinued;
its lists therefore do not represent the entire manga/manhwa market.

## Meaning and ranking

- Popular is reciprocal rank fusion of provider lists: each source contributes
  `1 / (10 + position)` for a card. This is an internal ordering score, never
  presented as views or reader counts. Actual MangaDex followers and ratings
  keep their provider label. WeebCentral weekly positions are not compared as
  numeric views with MangaRead's unspecified-window popularity.
- Recent means latest chapter releases, sorted by valid published timestamps
  where supplied. MangaDex is filtered to English and includes publisher links
  as well as hosted chapters. Future/malformed timestamps are ignored.
- Newly added means new to a provider catalog. It does **not** claim that the
  series began publication recently. Only real catalog creation timestamps are
  shown; publication years are never repurposed as addition dates.
- Conservative normalized exact-title matches combine source signals and use
  MangaDex metadata first. Distinct editions retain qualifiers. Identical titles
  within one provider are not automatically collapsed. Alternate translations
  can remain separate; this is intentionally safer than fuzzy merging editions.
- Each section contains at most 24 cards. The spotlight interleaves up to 12
  unique titles from the top 16 candidates of each section, with an hourly
  deterministic offset. The browser chooses a different opening offset per
  visit. This provides variety grounded in the lists, not personalization.

## Cost, caching and outages

Feeds are fetched on demand, shared across visitors, and cached for five minutes.
Shared homepage HTML is cached for a minute, MangaDex's existing API cache and
quota apply, and no per-card detail/chapter requests are made for the spotlight.
A one-minute aggregate cache and one-minute failure cache coalesce concurrent
requests. Caches have fixed capacities. Existing provider concurrency permits,
body limits, URL validation, timeouts, and image allowlists still apply.

Successful provider feeds can be retained for up to 30 minutes during an outage,
explicitly marked stale in `sources`. A failed source does not discard the
remaining feeds. Complete failure returns the normal unavailable response.
The browser refreshes one shared discovery request every five minutes while
visible, refreshes when a stale page becomes visible, cancels on unmount, and
keeps its existing cards on a refresh failure. It offers retry and source status
under **About these picks**. Manhwa and Manhua carousels remain available.

The response preserves `slug`, `title`, `image`, `imageFallback`, and `chapters`;
it adds signals, optional release/catalog dates, bounded descriptions, sections,
source availability, spotlight kind/reason and a snapshot check time. Covers use the same restricted
image delivery/fallback resolver as other pages. No new external proxy targets,
API secrets, paid services, databases or personal tracking are introduced.

## Validation and deployment

Offline parser fixtures contain small sanitized excerpts of real provider
responses, without scripts or ad iframes. Unit tests cover merging, edition
separation, dates, hourly spotlight variety, bounded stale fallback, failure
caching, partial outages, hosted/external English feeds and real statistic fields.
`LIVE_DISCOVERY=1 go test ./scrapers -run '^TestLiveDiscovery$' -count=1 -v`
checks public provider responses without downloading chapter images.

Run backend tests/race/vet and Windows cross-build, then frontend lint/build and
all browser regressions including `npm run test:discovery`. Deploy the versioned
Windows release before the frontend. Verify `/api/manga/discovery` through Vercel
carries the new PC backend ID, source statuses and populated sections. Existing
Windows rollback instructions apply; only AnyMangaBackend may be restarted.
If reverting to a backend without `/discovery`, roll back the frontend first.
