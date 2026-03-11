package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	golog "log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/dresswithpockets/go-steam-openid-example/db"
	"github.com/dresswithpockets/go-steam-openid-example/env"
	"github.com/dresswithpockets/go-steam-openid-example/log"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/rotisserie/eris"
	"github.com/yohcop/openid-go"
)

const (
	SessionCookieName = "sessionid"

	SessionIssuer       = "jump"
	SessionAudience     = "jump"
	SessionDuration     = time.Hour * 24 * 7
	SessionJitter       = time.Minute
	PrincipalContextKey = "principal"

	SteamOidcIssuer      = "https://steamcommunity.com/openid/"
	SteamOidRedirectPath = "/signin/callback"
)

var (
	SessionTokenSecret  = []byte("blahblahblah")
	SessionCookieSecure = false
	OidRealm            = ""
	OidRealmURL         = &url.URL{}
	SteamApiKey         = ""

	discoveryCache = &NoOpDiscoveryCache{}
)

type Principal struct {
	SteamID uint64
	TokenID uuid.UUID
	Claims  *jwt.RegisteredClaims
}

func GetPrincipal(ctx context.Context) (result *Principal, ok bool) {
	result, ok = ctx.Value(PrincipalContextKey).(*Principal)
	ok = ok && result != nil
	return
}

func HasPrincipal(ctx context.Context) bool {
	result, ok := ctx.Value(PrincipalContextKey).(*Principal)
	return ok && result != nil
}

type DiscoverInput struct {
	URL url.URL
}

func (d *DiscoverInput) Resolve(ctx huma.Context) []error {
	d.URL = ctx.URL()
	return nil
}

type DiscoverOutput struct {
	Status int
	Url    string `header:"Location"`
}

func handleSteamDiscover(ctx context.Context, input *DiscoverInput) (*DiscoverOutput, error) {
	callbackURL := OidRealmURL.JoinPath(SteamOidRedirectPath)
	redirectUrl, err := openid.RedirectURL(SteamOidcIssuer, callbackURL.String(), OidRealm)

	if err != nil {
		log.Logger.ErrorContext(ctx, "Error creating openid redirect", "err", err)
		return nil, eris.Wrap(err, "Error creating openid redirect")
	}

	return &DiscoverOutput{
		Status: http.StatusOK,
		Url:    redirectUrl,
	}, nil
}

type CallbackInput struct {
	Ns            string `query:"openid.ns"`
	Mode          string `query:"openid.mode"`
	OpEndpoint    string `query:"openid.op_endpoint"`
	ClaimedId     string `query:"openid.claimed_id"`
	Identity      string `query:"openid.identity"`
	ReturnTo      string `query:"openid.return_to"`
	ResponseNonce string `query:"openid.response_nonce"`
	AssocHandle   string `query:"openid.assoc_handle"`
	Signed        string `query:"openid.signed"`
	Sig           string `query:"openid.sig"`
}

type Callback struct {
	JWT string `json:"jwt"`
}

type CallbackOutput struct {
	Body Callback
}

func handleSteamCallback(ctx context.Context, input *CallbackInput) (*CallbackOutput, error) {
	// TUTORIAL: our openid library verifies that the original request came from our authority, but it needs us to
	//           provide a URL to verify that the incoming callback request has the authority we expect. Here we're
	//           just replacing the `https://blahblah.com` part of the URL with our OidRealm

	values := make(url.Values)
	values.Set("openid.ns", input.Ns)
	values.Set("openid.mode", input.Mode)
	values.Set("openid.op_endpoint", input.OpEndpoint)
	values.Set("openid.claimed_id", input.ClaimedId)
	values.Set("openid.identity", input.Identity)
	values.Set("openid.return_to", input.ReturnTo)
	values.Set("openid.response_nonce", input.ResponseNonce)
	values.Set("openid.assoc_handle", input.AssocHandle)
	values.Set("openid.signed", input.Signed)
	values.Set("openid.sig", input.Sig)

	fullUrl := OidRealmURL.JoinPath(SteamOidRedirectPath)
	fullUrl.RawQuery = values.Encode()

	fmt.Println("fullUrl: ", fullUrl.String())

	// TUTORIAL: verify the openid callback. The discovery cache caches some response information to make verification
	//           faster if the callback is hit with the same user again. The nonce store ensures that a callback request
	//           is never processed by our servers more than once.
	id, err := openid.Verify(fullUrl.String(), discoveryCache, db.NewNonceStore(ctx, db.Queries))
	if err != nil {
		log.Logger.Debug("Error verifying openid callback", "error", err, "uri", fullUrl)
		return nil, eris.Wrap(err, "Error verifying openid callback")
	}

	log.Logger.Debug("Verified openid callback for steam user", "user_id", id)

	// TUTORIAL: the openid id is in the format `https?://steamcommunity.com/openid/id/[0-9]+`. We only care about the
	//           last part, which is the user's Steam ID 64.
	var steamID string
	if strings.HasPrefix(id, "https") {
		_, err = fmt.Sscanf(id, "https://steamcommunity.com/openid/id/%s", &steamID)
	} else {
		_, err = fmt.Sscanf(id, "http://steamcommunity.com/openid/id/%s", &steamID)
	}

	if err != nil {
		log.Logger.Error("Verified openid callback but couldn't parse Steam ID 64 from ID.", "id", id, "error", err)
		return nil, eris.Wrap(err, "Error parsing Steam ID 64 from ID")
	}

	// TUTORIAL: we need the steam ID as a string, but we want to ensure that it is a valid uint64 first
	_, parseErr := strconv.ParseUint(steamID, 10, 64)
	if parseErr != nil {
		log.Logger.Error("Verified openid callback but couldn't parse Steam ID 64 from ID.", "id", id, "error", err)
		return nil, eris.Wrap(err, "Error parsing Steam ID 64")
	}

	// TUTORIAL: if the user doesn't already exist in the database, we need to ensure they exist
	dbErr := db.Queries.AddUserIgnoreConflict(ctx, steamID)
	if dbErr != nil {
		return nil, eris.Wrap(dbErr, "Error creating new user")
	}

	// TUTORIAL: AddSession will create a new session token entry and link it to the user. It will also generate a
	//           UUIDv7 TokenID for us, because `session.token_id` has a default value of `uuidv7()`
	session, dbErr := db.Queries.AddSession(ctx, steamID)
	if dbErr != nil {
		return nil, eris.Wrap(dbErr, "Error creating new session")
	}

	// TUTORIAL: There are a handful of "claims" we need to specify in the JWT. The subject and ID are the most
	//           important, since they specify the user's authenticated steam ID and the token's UUID.
	expiresAt := session.CreatedAt.Add(SessionDuration)
	claims := jwt.RegisteredClaims{
		Issuer:    SessionIssuer,
		Subject:   steamID,
		Audience:  []string{SessionAudience},
		ExpiresAt: jwt.NewNumericDate(expiresAt),
		NotBefore: jwt.NewNumericDate(session.CreatedAt.Add(-SessionJitter)),
		IssuedAt:  jwt.NewNumericDate(session.CreatedAt),
		ID:        session.TokenID.String(),
	}

	// TUTORIAL: this is creating and signing the actual JWT with the claims & secret we provide.
	signedJwt, jwtErr := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(SessionTokenSecret)
	if jwtErr != nil {
		return nil, eris.Wrap(jwtErr, "Error creating new session")
	}

	// TUTORIAL: and finally, setting the session cookie with our session JWT!
	return &CallbackOutput{
		Body: Callback{
			JWT: signedJwt,
		},
	}, nil
}

type SteamProfile struct {
	SteamID         string `json:"steamId"`
	PersonaName     string `json:"personaName"`
	ProfileURL      string `json:"profileUrl"`
	AvatarURL       string `json:"avatarUrl"`
	AvatarMediumURL string `json:"avatarMediumUrl"`
	AvatarFullURL   string `json:"avatarFullUrl"`
}

func SteamProfileFromSummary(summary PlayerSummary) SteamProfile {
	return SteamProfile{
		SteamID:         summary.SteamID,
		PersonaName:     summary.PersonaName,
		ProfileURL:      summary.ProfileURL,
		AvatarURL:       summary.AvatarURL,
		AvatarMediumURL: summary.AvatarMediumURL,
		AvatarFullURL:   summary.AvatarFullURL,
	}
}

type SteamProfileOutput struct {
	Body SteamProfile
}

//goland:noinspection SpellCheckingInspection
type PlayerSummary struct {
	SteamID                  string `json:"steamid"`
	CommunityVisibilityState int    `json:"communityvisibilitystate"`
	ProfileState             int    `json:"profilestate"`
	PersonaName              string `json:"personaname"`
	CommentPermission        int    `json:"commentpermission"`
	ProfileURL               string `json:"profileurl"`
	AvatarURL                string `json:"avatar"`
	AvatarMediumURL          string `json:"avatarmedium"`
	AvatarFullURL            string `json:"avatarfull"`
	AvatarHash               string `json:"avatarhash"`
	PersonaState             int    `json:"personastate"`
	PrimaryClanID            string `json:"primaryclanid"`
	TimeCreated              int    `json:"timecreated"`
	PersonaStateFlags        int    `json:"personastateflags"`
	CountryCode              string `json:"loccountrycode"`
}

type PlayerSummaries struct {
	Response struct {
		Players []PlayerSummary
	}
}

func handleSteamProfile(ctx context.Context, _ *struct{}) (*SteamProfileOutput, error) {
	// TUTORIAL: if we don't have a principal, that means the user is not signed in or their session has expired.
	principal, ok := GetPrincipal(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("a session is required")
	}

	// TUTORIAL: the ISteamUser::GetPlayerSummaries API requires a steam API key...
	encodedKey := url.QueryEscape(SteamApiKey)
	encodedSteamID := url.QueryEscape(strconv.FormatUint(principal.SteamID, 10))
	summaryUrl := fmt.Sprintf("https://api.steampowered.com/ISteamUser/GetPlayerSummaries/v2?key=%s&steamids=%s", encodedKey, encodedSteamID)

	// TUTORIAL: retryablehttp uses an exponential backoff by default. If the first request fails, it will retry
	//           continuously with longer and longer periods between each retry, to avoid rate limiting.
	httpResponse, err := retryablehttp.Get(summaryUrl)
	if err != nil {
		return nil, eris.Wrap(err, "Error getting steam player summaries")
	}

	// TUTORIAL: just reading the response body has bytes.
	bodyBytes, readErr := io.ReadAll(httpResponse.Body)
	if readErr != nil {
		return nil, eris.Wrap(readErr, "Error reading steam player summary response body")
	}

	// TUTORIAL: parsing the body, assuming that it is valid unicode JSON.
	var summaries PlayerSummaries
	jsonErr := json.Unmarshal(bodyBytes, &summaries)
	if jsonErr != nil {
		return nil, eris.Wrap(jsonErr, "Error parsing player summary response body")
	}

	// TUTORIAL: we only provided one steamID in the request, we should only get one in the response.
	if len(summaries.Response.Players) != 1 {
		return nil, huma.Error500InternalServerError("Unexpected player summaries in steam response")
	}

	// TUTORIAL: map the steam response to only the fields we care about.
	return &SteamProfileOutput{
		Body: SteamProfileFromSummary(summaries.Response.Players[0]),
	}, nil
}

type SignOutOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

func handleSteamSignOut(ctx context.Context, _ *struct{}) (*SignOutOutput, error) {
	// TUTORIAL: if we don't have a principal, that means the user is not signed in or their session has expired.
	principal, ok := GetPrincipal(ctx)
	if !ok {
		return nil, huma.Error401Unauthorized("a session is required")
	}

	// TUTORIAL: but, if we do have a session, we should forcefully invalidate the session to ensure the user's token
	//           can't be re-used. TokenIDs are UUIDv7, which have a time-based monotonic counter as part of the ID...
	//           as a result, it's virtually impossible for the same token to be generated twice.
	err := db.Queries.DisallowToken(ctx, principal.TokenID)
	if err != nil {
		return nil, eris.Wrap(err, "Error signing out session")
	}

	// TUTORIAL: then we expire their session cookie.
	return &SignOutOutput{
		SetCookie: http.Cookie{
			Name:     SessionCookieName,
			Path:     "/",
			Value:    "",
			MaxAge:   0,
			Expires:  time.Now(),
			Secure:   SessionCookieSecure,
			SameSite: http.SameSiteStrictMode,
		},
	}, nil
}

func RegisterRoutes(api huma.API) {
	OidRealm = env.GetString("JUMP_OID_REALM")
	oidRealmURL, realmUrlErr := url.Parse(OidRealm)
	if realmUrlErr != nil {
		golog.Fatalf("Error parsing JUMP_OID_REALM: %v", realmUrlErr)
	}

	OidRealmURL = oidRealmURL
	SessionTokenSecret = []byte(env.GetString("JUMP_SESSION_TOKEN_SECRET"))
	SessionCookieSecure = env.GetBool("JUMP_SESSION_COOKIE_SECURE")
	SteamApiKey = env.GetString("JUMP_STEAM_API_KEY")

	internalApi := huma.NewGroup(api, "/internal")
	// TUTORIAL: if you have other routes you want under /internal, you'd put them here
	// TUTORIAL: if you want to group endpoints together in the doc, see the "Tags" property in huma.Operation

	sessionApi := huma.NewGroup(internalApi, "/session")

	// TUTORIAL: the OpenID flow works like this:
	//           - The user is redirected to `/internal/session/steam/discover`
	//           - /internal/session/steam/discover does some magic & redirects the user to the Steam OpenID auth flow
	//           - Once the user logs in, steam redirects the user to `/internal/session/steam/callback`,
	//             with some information about the user's auth session
	//           - `/internal/session/steam/callback` creates a new session token for the user
	//           - the user is redirected back to home with their session cookies set
	huma.Get(sessionApi, "/steam/discover", handleSteamDiscover)
	huma.Get(sessionApi, "/steam/callback", handleSteamCallback)

	var sessionCookieSecurityMap = []map[string][]string{{"Steam": {}}}
	var requireUserSessionMiddlewares = huma.Middlewares{UserAuthHandler, CreateRequireUserAuthHandler(internalApi)}

	huma.Register(sessionApi, huma.Operation{
		Method:      http.MethodGet,
		Path:        "/steam/profile",
		OperationID: "steam-profile",
		Summary:     "Steam profile",
		Description: "Get the authenticated user's steam profile info",
		Errors:      []int{http.StatusUnauthorized},

		Security:    sessionCookieSecurityMap,
		Middlewares: requireUserSessionMiddlewares,
	}, handleSteamProfile)

	huma.Register(sessionApi, huma.Operation{
		Method:      http.MethodGet,
		Path:        "/sign-out",
		OperationID: "sign-out",
		Summary:     "Sign out",
		Description: "Sign out & clear session",
		Errors:      []int{http.StatusUnauthorized},

		Security:    sessionCookieSecurityMap,
		Middlewares: requireUserSessionMiddlewares,
	}, handleSteamSignOut)
}
