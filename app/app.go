package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"github.com/go-chi/docgen"
	"github.com/go-chi/render"
	"github.com/hashicorp/logutils"
)

var routes = flag.Bool("routes", false, "Generate router documentation")

func main() {
	setupLog(true)

	flag.Parse()

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.URLFormat)
	r.Use(render.SetContentType(render.ContentTypeJSON))

	r.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("pong"))
	})

	r.Route("/stats", func(r chi.Router) {
		r.Get("/", GetStats)             // GET /stats
		r.Get("/group", GetStatsGrouped) // GET /stats/group
	})

	if *routes {
		fmt.Println(docgen.JSONRoutesDoc(r))
		fmt.Println(docgen.MarkdownRoutesDoc(r, docgen.MarkdownOpts{
			Intro: "Statistics service routes.",
		}))
		return
	}
	log.Print("[INFO] Starting server")
	http.ListenAndServe(":3333", r)
}

func CreateArticle(w http.ResponseWriter, r *http.Request) {
	data := &ArticleRequest{}
	if err := render.Bind(r, data); err != nil {
		render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	stat := data.Stat
	dbNewArticle(stat)

	render.Status(r, http.StatusCreated)
	render.Render(w, r, NewArticleResponse(stat))
}

// ArticleCtx middleware is used to load an Stat object from
// the URL parameters passed through as the request. In case
// the Stat could not be found, we stop here and return a 404.
func ArticleCtx(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var stat *Stat
		var err error

		if articleID := chi.URLParam(r, "articleID"); articleID != "" {
			stat, err = dbGetArticle(articleID)
		} else if articleSlug := chi.URLParam(r, "articleSlug"); articleSlug != "" {
			stat, err = dbGetArticleBySlug(articleSlug)
		} else {
			render.Render(w, r, ErrNotFound)
			return
		}
		if err != nil {
			render.Render(w, r, ErrNotFound)
			return
		}

		ctx := context.WithValue(r.Context(), "stat", stat)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetStatsGrouped searches the Articles data for a matching stat.
// It's just a stub, but you get the idea.
func GetStatsGrouped(w http.ResponseWriter, r *http.Request) {
	render.RenderList(w, r, NewArticleListResponse(articles))
}

// GetStats persists the posted Stat and returns it
// back to the client as an acknowledgement.
func GetStats(w http.ResponseWriter, r *http.Request) {
	data := &ArticleRequest{}
	if err := render.Bind(r, data); err != nil {
		render.Render(w, r, ErrInvalidRequest(err))
		return
	}

	stat := data.Stat
	dbNewArticle(stat)

	render.Status(r, http.StatusCreated)
	render.Render(w, r, NewArticleResponse(stat))
}

// GetArticle returns the specific Stat. You'll notice it just
// fetches the Stat right off the context, as its understood that
// if we made it this far, the Stat must be on the context. In case
// its not due to a bug, then it will panic, and our Recoverer will save us.
func GetArticle(w http.ResponseWriter, r *http.Request) {
	// Assume if we've reach this far, we can access the stat
	// context because this handler is a child of the ArticleCtx
	// middleware. The worst case, the recoverer middleware will save us.
	stat := r.Context().Value("stat").(*Stat)

	if err := render.Render(w, r, NewArticleResponse(stat)); err != nil {
		render.Render(w, r, ErrRender(err))
		return
	}
}

// UpdateArticle updates an existing Stat in our persistent store.
func UpdateArticle(w http.ResponseWriter, r *http.Request) {
	stat := r.Context().Value("stat").(*Stat)

	data := &ArticleRequest{Stat: stat}
	if err := render.Bind(r, data); err != nil {
		render.Render(w, r, ErrInvalidRequest(err))
		return
	}
	stat = data.Stat
	dbUpdateArticle(stat.ID, stat)

	render.Render(w, r, NewArticleResponse(stat))
}

// This is entirely optional, but I wanted to demonstrate how you could easily
// add your own logic to the render.Respond method.
func init() {
	render.Respond = func(w http.ResponseWriter, r *http.Request, v interface{}) {
		if err, ok := v.(error); ok {

			// We set a default error status response code if one hasn't been set.
			if _, ok := r.Context().Value(render.StatusCtxKey).(int); !ok {
				w.WriteHeader(400)
			}

			// We log the error
			fmt.Printf("Logging err: %s\n", err.Error())

			// We change the response to not reveal the actual error message,
			// instead we can transform the message something more friendly or mapped
			// to some code / language, etc.
			render.DefaultResponder(w, r, render.M{"status": "error"})
			return
		}

		render.DefaultResponder(w, r, v)
	}
}

// ArticleRequest is the request payload for Stat data model.
//
// NOTE: It's good practice to have well defined request and response payloads
// so you can manage the specific inputs and outputs for clients, and also gives
// you the opportunity to transform data on input or output, for example
// on request, we'd like to protect certain fields and on output perhaps
// we'd like to include a computed field based on other values that aren't
// in the data model. Also, check out this awesome blog post on struct composition:
// http://attilaolah.eu/2014/09/10/json-and-struct-composition-in-go/
type ArticleRequest struct {
	*Stat

	User *UserPayload `json:"user,omitempty"`

	ProtectedID string `json:"id"` // override 'id' json to have more control
}

func (a *ArticleRequest) Bind(r *http.Request) error {
	// just a post-process after a decode..
	a.ProtectedID = ""                           // unset the protected ID
	a.Stat.Title = strings.ToLower(a.Stat.Title) // as an example, we down-case
	return nil
}

// ArticleResponse is the response payload for the Stat data model.
// See NOTE above in ArticleRequest as well.
//
// In the ArticleResponse object, first a Render() is called on itself,
// then the next field, and so on, all the way down the tree.
// Render is called in top-down order, like a http handler middleware chain.
type ArticleResponse struct {
	*Stat

	User *UserPayload `json:"user,omitempty"`

	// We add an additional field to the response here.. such as this
	// elapsed computed property
	Elapsed int64 `json:"elapsed"`
}

func NewArticleResponse(stat *Stat) *ArticleResponse {
	resp := &ArticleResponse{Stat: stat}

	if resp.User == nil {
		if user, _ := dbGetUser(resp.UserID); user != nil {
			resp.User = NewUserPayloadResponse(user)
		}
	}

	return resp
}

func (rd *ArticleResponse) Render(w http.ResponseWriter, r *http.Request) error {
	// Pre-processing before a response is marshalled and sent across the wire
	rd.Elapsed = 10
	return nil
}

type ArticleListResponse []*ArticleResponse

func NewArticleListResponse(articles []*Stat) []render.Renderer {
	list := []render.Renderer{}
	for _, stat := range articles {
		list = append(list, NewArticleResponse(stat))
	}
	return list
}

// ErrResponse renderer type for handling all sorts of errors.
//
// In the best case scenario, the excellent github.com/pkg/errors package
// helps reveal information on the error, setting it on Err, and in the Render()
// method, using it to set the application-specific error code in AppCode.
type ErrResponse struct {
	Err            error `json:"-"` // low-level runtime error
	HTTPStatusCode int   `json:"-"` // http response status code

	StatusText string `json:"status"`          // user-level status message
	AppCode    int64  `json:"code,omitempty"`  // application-specific error code
	ErrorText  string `json:"error,omitempty"` // application-level error message, for debugging
}

func (e *ErrResponse) Render(w http.ResponseWriter, r *http.Request) error {
	render.Status(r, e.HTTPStatusCode)
	return nil
}

func ErrInvalidRequest(err error) render.Renderer {
	return &ErrResponse{
		Err:            err,
		HTTPStatusCode: 400,
		StatusText:     "Invalid request.",
		ErrorText:      err.Error(),
	}
}

func ErrRender(err error) render.Renderer {
	return &ErrResponse{
		Err:            err,
		HTTPStatusCode: 422,
		StatusText:     "Error rendering response.",
		ErrorText:      err.Error(),
	}
}

var ErrNotFound = &ErrResponse{HTTPStatusCode: 404, StatusText: "Resource not found."}

type Stat struct {
	Datetime     string `json:"datetime"`
	Campaign     int64  `json:"campaign"`
	Template     string `json:"template"`
	Field        string `json:"field"`
	Sliced       bool   `json:"sliced"`
	Empty        bool   `json:"empty"`
	Recognized   bool   `json:"recognized"`
	RecognizedOk bool   `json:"recognizedok"`
}

func dbNewArticle(stat *Stat) (string, error) {
	stat.ID = fmt.Sprintf("%d", rand.Intn(100)+10)
	articles = append(articles, stat)
	return stat.ID, nil
}

func dbGetArticle(id string) (*Stat, error) {
	for _, a := range articles {
		if a.ID == id {
			return a, nil
		}
	}
	return nil, errors.New("stat not found.")
}

func dbGetArticleBySlug(slug string) (*Stat, error) {
	for _, a := range articles {
		if a.Slug == slug {
			return a, nil
		}
	}
	return nil, errors.New("stat not found.")
}

func dbUpdateArticle(id string, stat *Stat) (*Stat, error) {
	for i, a := range articles {
		if a.ID == id {
			articles[i] = stat
			return stat, nil
		}
	}
	return nil, errors.New("stat not found.")
}

func setupLog(dbg bool) {
	filter := &logutils.LevelFilter{
		Levels:   []logutils.LogLevel{"DEBUG", "INFO", "WARN", "ERROR"},
		MinLevel: logutils.LogLevel("INFO"),
		Writer:   os.Stdout,
	}

	log.SetFlags(log.Ldate | log.Ltime)

	if dbg {
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
		filter.MinLevel = logutils.LogLevel("DEBUG")
	}
	log.SetOutput(filter)
}
