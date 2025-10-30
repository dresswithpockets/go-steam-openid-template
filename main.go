package main

import (
	"context"

	"github.com/dresswithpockets/go-steam-openid-example/db"
	"github.com/dresswithpockets/go-steam-openid-example/env"
	"github.com/dresswithpockets/go-steam-openid-example/internal"
	"github.com/dresswithpockets/go-steam-openid-example/log"

	golog "log"
	"log/slog"
	"net/http"
	"os"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httplog/v3"
	"github.com/rotisserie/eris"
	"github.com/rs/cors"
)

func setupRouter() (*chi.Mux, error) {
	// TUTORIAL: we're configuring our logger to always log in the ECS v9 schema.
	//           this schema is easily consumed by many observability/telemetry tools.
	logConcise := env.GetBool("JUMP_HTTPLOG_CONCISE")
	logFormat := httplog.SchemaECS.Concise(logConcise)

	logLevel, matchedErr := env.GetMapped("JUMP_HTTPLOG_LEVEL", log.SlogLevelMap)
	if matchedErr != nil {
		return nil, matchedErr
	}

	handlerOptions := &slog.HandlerOptions{
		ReplaceAttr: logFormat.ReplaceAttr,
		Level:       logLevel,
	}

	// TUTORIAL: this logger is later used to create an http logger, which chi uses to
	//           log requests & errors. The logger can output in either plain text or JSON.
	var logger *slog.Logger
	slogMode := env.GetString("JUMP_HTTPLOG_MODE")
	switch slogMode {
	case "Text":
		logger = slog.New(slog.NewTextHandler(os.Stdout, handlerOptions))
	case "JSON":
		logger = slog.New(slog.NewJSONHandler(os.Stdout, handlerOptions))
	default:
		return nil, eris.Errorf("invalid value for JUMP_HTTPLOG_Mode: %s", slogMode)
	}

	router := chi.NewMux()

	options := &httplog.Options{
		// Level defines the verbosity of the request logs:
		// slog.LevelDebug - log all responses (incl. OPTIONS)
		// slog.LevelInfo  - log responses (excl. OPTIONS)
		// slog.LevelWarn  - log 4xx and 5xx responses only (except for 429)
		// slog.LevelError - log 5xx responses only
		Level: logLevel,

		// Set log output to Elastic Common Schema (ECS) format.
		Schema: logFormat,

		// RecoverPanics recovers from panics occurring in the underlying HTTP handlers
		// and middlewares. It returns HTTP 500 unless response status was already set.
		//
		// NOTE: Panics are logged as errors automatically, regardless of this setting.
		RecoverPanics: true,

		// Optionally, log selected request/response headers explicitly.
		LogRequestHeaders:  env.GetList("JUMP_HTTPLOG_REQUEST_HEADERS"),
		LogResponseHeaders: env.GetList("JUMP_HTTPLOG_RESPONSE_HEADERS"),
	}

	if env.GetBool("JUMP_HTTPLOG_REQUEST_BODIES") {
		options.LogRequestBody = func(r *http.Request) bool { return true }
	}

	if env.GetBool("JUMP_HTTPLOG_RESPONSE_BODIES") {
		options.LogResponseBody = func(r *http.Request) bool { return true }
	}

	// TUTORIAL: every request should get logged using our configured logger
	router.Use(httplog.RequestLogger(logger, options))

	// TUTORIAL: configure CORS middleware to restrict what origins can send requests,
	//           and allow credentials in requests. `AllowedOrigins` should be configured more
	//           strictly before deploying to production.
	router.Use(cors.New(cors.Options{
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowCredentials: true,
		AllowedOrigins:   []string{"*"}, // this is the default value
	}).Handler)

	// TODO: CSRF middleware
	// TODO: rate limit middleware

	return router, nil
}

func setupHuma(router chi.Router) huma.API {
	// TUTORIAL: Create a new huma config with some basic information. feel free to change
	//           this as you see fit. The Contact and License are not required, but I always
	//           fill them in anyways.
	humaConfig := huma.DefaultConfig("jump", "1.0.0")
	humaConfig.Info = &huma.Info{
		Title:       "jump",
		Description: "internal jump API docs",
		Contact: &huma.Contact{
			Name: "spiritov",
			URL:  "https://github.com/spiritov",
		},
		License: &huma.License{
			Name:       "GPL General Public License v3",
			Identifier: "GPL-3.0-or-later",
			URL:        "https://spdx.org/licenses/GPL-3.0-or-later.html",
		},
		Version: "v1.0.0",
	}

	// TUTORIAL: These are the OpenAPI security schemes you plan on using. Since you're only
	//           using Steam OpenID auth, you're only going to have one "Steam" security scheme.
	//           This will basically always be a JWT, with the user's OpenID information.
	humaConfig.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"Steam": {
			Type:        "apiKey",
			In:          "cookie",
			Description: "A session cookie is used to store the user's session token.",
			Name:        internal.SessionCookieName,
		},
	}

	// TUTORIAL: once we've configured a router & the huma config, we can create a new huma API.
	return humachi.New(router, humaConfig)
}

func main() {
	// TUTORIAL: the env package wraps the `godotenv` library, which reads .env files based on
	//           the value of the environment variable named via the envKey parameter.
	if err := env.Load("JUMP_ENV"); err != nil {
		golog.Fatalf("Error loading envvars from .env files: %v", err)
	}

	// TUTORIAL: there are many environment variables that _must_ be provided in order for this
	//           application to run. If some don't exist, we crash.
	env.Require(
		"JUMP_DB_HOST",
		"JUMP_DB_PORT",
		"JUMP_DB_NAME",
		"JUMP_DB_USERNAME",
		"JUMP_DB_PASSWORD",
		"JUMP_DB_TRACE_LOG",
		"JUMP_SLOG_LEVEL",
		"JUMP_SLOG_MODE",
		"JUMP_HTTP_ADDR",
		"JUMP_HTTPLOG_LEVEL",
		"JUMP_HTTPLOG_MODE",
		"JUMP_HTTPLOG_CONCISE",
		"JUMP_HTTPLOG_REQUEST_HEADERS",
		"JUMP_HTTPLOG_RESPONSE_HEADERS",
		"JUMP_HTTPLOG_REQUEST_BODIES",
		"JUMP_HTTPLOG_RESPONSE_BODIES",
		"JUMP_SESSION_TOKEN_SECRET",
		"JUMP_SESSION_COOKIE_SECURE",
		"JUMP_STEAM_API_KEY",
		"JUMP_OID_REALM",
	)

	// TUTORIAL: now that we've ensured that our required envvars exist, we can setup some packages.
	//           the log package just exists to store a global *slog.Logger that we use everywhere except
	//           our entry point.
	if err := log.Setup(); err != nil {
		golog.Fatal(err)
	}

	// TUTORIAL: we also need to setup our connection pool to our database. I'm using postgres for this
	//           template, but you can totally use sqlite or something else.
	if err := db.SetupDB(context.Background()); err != nil {
		golog.Fatal(err)
	}

	// TUTORIAL: initialize the chi router that huma will use. If this fails, we must exit.
	router, routerErr := setupRouter()
	if routerErr != nil {
		golog.Fatal(routerErr)
	}

	api := setupHuma(router)

	// TUTORIAL: A readiness endpoint is important - it can be used to inform your infrastructure
	//           (e.g. fly.io) that the API is available. Readiness checks can help keep your API
	//           alive, by informing fly on when it should try restarting a machine in case of a
	//           crash.
	type ReadyResponse struct{ OK bool }
	huma.Register(api, huma.Operation{
		OperationID: "readyz",
		Method:      http.MethodGet,
		Path:        "/readyz",
		Summary:     "Get Readiness",
		Description: "Get whether or not the API is ready to process requests",
		Tags:        []string{"Health Check"},
	}, func(ctx context.Context, _ *struct{}) (*ReadyResponse, error) {
		return &ReadyResponse{OK: true}, nil
	})

	// TUTORIAL: finally registering our actual internal API routes
	internal.RegisterRoutes(api)

	// TUTORIAL: and serving the API on the address in JUMP_HTTP_ADDR, which should be something
	//           like "localhost:8080"
	address := env.GetString("JUMP_HTTP_ADDR")
	if err := http.ListenAndServe(address, router); err != nil {
		golog.Fatal(err)
	}
}
