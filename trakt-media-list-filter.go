// main.go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "os"
    "os/exec"
    "strconv"
    "strings"
    "time"

    "github.com/go-resty/resty/v2"
    "github.com/spf13/cobra"
)

const (
    configFile  = "config.json"
    tokenFile   = "trakt_token.json"
    redirectURI = "http://localhost:8000"
)

// ---------- CONFIG ----------

type configManager struct {
    config map[string]string
    tokens map[string]interface{}
}

func newConfigManager() *configManager {
    cm := &configManager{}
    cm.config = loadJSON(configFile).(map[string]string)
    cm.tokens = loadJSON(tokenFile).(map[string]interface{})
    return cm
}

func loadJSON(path string) interface{} {
    if _, err := os.Stat(path); os.IsNotExist(err) {
        return map[string]interface{}{}
    }
    b, err := os.ReadFile(path)
    if err != nil {
        return map[string]interface{}{}
    }
    var data interface{}
    _ = json.Unmarshal(b, &data)
    if data == nil {
        return map[string]interface{}{}
    }
    return data
}

func (cm *configManager) getConfig(key string) string {
    return cm.config[key]
}

func (cm *configManager) getToken(key string) string {
    if v, ok := cm.tokens[key].(string); ok {
        return v
    }
    return ""
}

func (cm *configManager) saveTokens(data map[string]interface{}) {
    data["created_at"] = time.Now().Unix()
    b, _ := json.MarshalIndent(data, "", "  ")
    _ = os.WriteFile(tokenFile, b, 0600)
    cm.tokens = data
}

func (cm *configManager) refreshNeeded() bool {
    exp, ok1 := cm.tokens["expires_in"].(float64)
    cat, ok2 := cm.tokens["created_at"].(float64)
    return !(ok1 && ok2 && time.Now().Unix() < int64(cat+exp))
}

// ---------- AUTH ----------

func getAccessToken(cm *configManager) string {
    if !cm.refreshNeeded() {
        return cm.getToken("access_token")
    }
    if cm.getToken("refresh_token") != "" {
        return refreshAccessToken(cm)
    }
    return authorizeFlow(cm)
}

func authorizeFlow(cm *configManager) string {
    clientID := cm.getConfig("CLIENT_ID")
    authURL := fmt.Sprintf(
        "https://api.trakt.tv/oauth/authorize?response_type=code&client_id=%s&redirect_uri=%s",
        clientID, redirectURI)

    srv := &http.Server{Addr: ":8000"}
    codeCh := make(chan string, 1)

    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        code := r.URL.Query().Get("code")
        if code != "" {
            codeCh <- code
        }
        w.Header().Set("Content-Type", "text/html")
        fmt.Fprint(w, "You may close this window.")
        go func() {
            time.Sleep(time.Second)
            _ = srv.Shutdown(context.Background())
        }()
    })

    fmt.Println("Opening browser for Trakt authorization...")
    openBrowser(authURL)
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
        log.Fatalf("oauth server: %v", err)
    }
    return exchangeCode(cm, <-codeCh)
}

func exchangeCode(cm *configManager, code string) string {
    client := resty.New()
    var res map[string]interface{}
    _, err := client.R().
        SetFormData(map[string]string{
            "code":          code,
            "client_id":     cm.getConfig("CLIENT_ID"),
            "client_secret": cm.getConfig("CLIENT_SECRET"),
            "redirect_uri":  redirectURI,
            "grant_type":    "authorization_code",
        }).
        SetResult(&res).
        Post("https://api.trakt.tv/oauth/token")
    if err != nil {
        log.Fatalf("token exchange: %v", err)
    }
    cm.saveTokens(res)
    return res["access_token"].(string)
}

func refreshAccessToken(cm *configManager) string {
    client := resty.New()
    var res map[string]interface{}
    _, err := client.R().
        SetFormData(map[string]string{
            "refresh_token": cm.getToken("refresh_token"),
            "client_id":     cm.getConfig("CLIENT_ID"),
            "client_secret": cm.getConfig("CLIENT_SECRET"),
            "grant_type":    "refresh_token",
        }).
        SetResult(&res).
        Post("https://api.trakt.tv/oauth/token")
    if err != nil {
        log.Fatalf("token refresh: %v", err)
    }
    cm.saveTokens(res)
    return res["access_token"].(string)
}

// ---------- DATA TYPES ----------

type personSearch []struct {
    Person struct {
        Name string `json:"name"`
        IDs  struct {
            Trakt int `json:"trakt"`
        } `json:"ids"`
    } `json:"person"`
}

type mediaItem struct {
    Movie *struct {
        Title string `json:"title"`
        Year  int    `json:"year"`
        IDs   struct {
            Trakt int `json:"trakt"`
        } `json:"ids"`
    } `json:"movie"`
    Show *struct {
        Title string `json:"title"`
        Year  int    `json:"year"`
        IDs   struct {
            Trakt int `json:"trakt"`
        } `json:"ids"`
    } `json:"show"`
    Character string `json:"character"`
    Job       string `json:"job"`
}

type filtered struct {
    Title string
    Year  int
    ID    int
    Type  string
    Role  string
}

// ---------- MAIN ----------

var cm *configManager

func main() {
    cm = newConfigManager()

    root := &cobra.Command{
        Use:   "trakt-media-filter",
        Short: "Filter Trakt movies/shows by person and role",
        Run:   run,
    }
    root.Flags().StringP("name", "n", "", "person name to search")
    root.Flags().IntP("trakt_id", "i", 0, "Trakt person ID to use directly")
    root.Flags().StringP("filter", "f", "", "role to filter (cast, director, writer...)")
    root.Flags().StringP("list-name", "l", "", "create/append to a Trakt list")
    root.Flags().Bool("movies-only", false, "limit list to movies")
    root.Flags().Bool("tv-only", false, "limit list to TV")
    root.Flags().Bool("all", false, "include both movies and TV")
    if err := root.Execute(); err != nil {
        log.Fatal(err)
    }
}

func run(cmd *cobra.Command, args []string) {
    name, _ := cmd.Flags().GetString("name")
    id, _ := cmd.Flags().GetInt("trakt_id")
    role, _ := cmd.Flags().GetString("filter")
    listName, _ := cmd.Flags().GetString("list-name")
    moviesOnly, _ := cmd.Flags().GetBool("movies-only")
    tvOnly, _ := cmd.Flags().GetBool("tv-only")
    includeAll, _ := cmd.Flags().GetBool("all")

    // default to include all if none specified
    if !moviesOnly && !tvOnly && !includeAll {
        includeAll = true
    }

    token := getAccessToken(cm)
    client := resty.New().
        SetAuthToken(token).
        SetHeader("trakt-api-version", "2").
        SetHeader("trakt-api-key", cm.getConfig("CLIENT_ID"))

    personID := id
    if name != "" {
        personID = choosePerson(client, name)
        if personID == 0 {
            os.Exit(1)
        }
    }

    items := fetchPersonMedia(client, personID)
    filtered := filterByRole(items, role, moviesOnly, tvOnly, includeAll)
    if listName == "" {
        for _, it := range filtered {
            fmt.Printf("%s (%d) – %s (%s)\n", it.Title, it.Year, it.Role, it.Type)
        }
        return
    }
    listID := createOrGetList(client, listName)
    likeList(client, listID)
    addMediaToList(client, listID, filtered)
}

func choosePerson(client *resty.Client, name string) int {
    var res personSearch
    _, err := client.R().
        SetQueryParams(map[string]string{"query": name}).
        SetResult(&res).
        Get("https://api.trakt.tv/search/person")
    if err != nil {
        log.Fatalf("search person: %v", err)
    }
    if len(res) == 0 {
        fmt.Println("No results.")
        return 0
    }
    for i, p := range res {
        known := knownFor(client, p.Person.IDs.Trakt)
        fmt.Printf("%d: %s – %s\n", i+1, p.Person.Name, known)
    }
    var choice int
    fmt.Print("Select number: ")
    _, _ = fmt.Scanln(&choice)
    if choice < 1 || choice > len(res) {
        return 0
    }
    return res[choice-1].Person.IDs.Trakt
}

func knownFor(client *resty.Client, personID int) string {
    var cast []mediaItem
    _, _ = client.R().
        SetResult(&cast).
        Get(fmt.Sprintf("https://api.trakt.tv/people/%d/movies?extended=full", personID))
    _, _ = client.R().
        SetResult(&cast).
        Get(fmt.Sprintf("https://api.trakt.tv/people/%d/shows?extended=full", personID))

    var titles []string
    for _, m := range cast {
        if m.Movie != nil {
            titles = append(titles, fmt.Sprintf("%s (%d)", m.Movie.Title, m.Movie.Year))
        }
        if m.Show != nil {
            titles = append(titles, fmt.Sprintf("%s (%d)", m.Show.Title, m.Show.Year))
        }
    }
    if len(titles) == 0 {
        return "N/A"
    }
    return strings.Join(titles, ", ")
}

func fetchPersonMedia(client *resty.Client, personID int) map[string]json.RawMessage {
    var raw map[string]json.RawMessage
    _, err := client.R().
        SetResult(&raw).
        Get(fmt.Sprintf("https://api.trakt.tv/people/%d/movies?extended=full", personID))
    if err != nil {
        log.Fatalf("fetch movies: %v", err)
    }
    _, err = client.R().
        SetResult(&raw).
        Get(fmt.Sprintf("https://api.trakt.tv/people/%d/shows?extended=full", personID))
    if err != nil {
        log.Fatalf("fetch shows: %v", err)
    }
    return raw
}

func filterByRole(raw map[string]json.RawMessage, role string, moviesOnly, tvOnly, all bool) []filtered {
    var res []filtered
    // cast
    var cast []mediaItem
    _ = json.Unmarshal(raw["cast"], &cast)
    for _, c := range cast {
        if c.Movie != nil {
            if tvOnly {
                continue
            }
            if role == "" || role == "cast" {
                res = append(res, filtered{
                    Title: c.Movie.Title,
                    Year:  c.Movie.Year,
                    ID:    c.Movie.IDs.Trakt,
                    Type:  "movie",
                    Role:  "cast: " + c.Character,
                })
            }
        }
        if c.Show != nil {
            if moviesOnly {
                continue
            }
            if role == "" || role == "cast" {
                res = append(res, filtered{
                    Title: c.Show.Title,
                    Year:  c.Show.Year,
                    ID:    c.Show.IDs.Trakt,
                    Type:  "show",
                    Role:  "cast: " + c.Character,
                })
            }
        }
    }
    // crew
    var crew map[string][]mediaItem
    _ = json.Unmarshal(raw["crew"], &crew)
    for dept, items := range crew {
        for _, c := range items {
            if c.Movie != nil {
                if tvOnly {
                    continue
                }
                if role == "" || strings.EqualFold(c.Job, role) {
                    res = append(res, filtered{
                        Title: c.Movie.Title,
                        Year:  c.Movie.Year,
                        ID:    c.Movie.IDs.Trakt,
                        Type:  "movie",
                        Role:  fmt.Sprintf("%s (%s)", c.Job, dept),
                    })
                }
            }
            if c.Show != nil {
                if moviesOnly {
                    continue
                }
                if role == "" || strings.EqualFold(c.Job, role) {
                    res = append(res, filtered{
                        Title: c.Show.Title,
                        Year:  c.Show.Year,
                        ID:    c.Show.IDs.Trakt,
                        Type:  "show",
                        Role:  fmt.Sprintf("%s (%s)", c.Job, dept),
                    })
                }
            }
        }
    }
    return res
}

func createOrGetList(client *resty.Client, name string) int {
    var lists []struct {
        Name string `json:"name"`
        IDs  struct {
            Trakt int `json:"trakt"`
        } `json:"ids"`
    }
    _, _ = client.R().SetResult(&lists).Get("https://api.trakt.tv/users/me/lists")
    for _, l := range lists {
        if strings.EqualFold(l.Name, name) {
            return l.IDs.Trakt
        }
    }
    payload := map[string]interface{}{
        "name":           name,
        "description":    "media filtered via trakt-media-filter",
        "privacy":        "private",
        "display_numbers": true,
        "allow_comments": false,
    }
    var newList struct {
        IDs struct {
            Trakt int `json:"trakt"`
        } `json:"ids"`
    }
    _, err := client.R().SetBody(payload).SetResult(&newList).Post("https://api.trakt.tv/users/me/lists")
    if err != nil {
        log.Fatalf("create list: %v", err)
    }
    return newList.IDs.Trakt
}

func likeList(client *resty.Client, listID int) {
    _, _ = client.R().Post(fmt.Sprintf("https://api.trakt.tv/users/me/lists/%d/like", listID))
}

func addMediaToList(client *resty.Client, listID int, items []filtered) {
    const batch = 10
    for i := 0; i < len(items); i += batch {
        end := i + batch
        if end > len(items) {
            end = len(items)
        }
        var moviesPayload []map[string]interface{}
        var showsPayload []map[string]interface{}
        for _, it := range items[i:end] {
            if it.Type == "movie" {
                moviesPayload = append(moviesPayload, map[string]interface{}{
                    "ids": map[string]int{"trakt": it.ID},
                })
            } else {
                showsPayload = append(showsPayload, map[string]interface{}{
                    "ids": map[string]int{"trakt": it.ID},
                })
            }
        }
        if len(moviesPayload) > 0 {
            _, _ = client.R().
                SetBody(map[string]interface{}{"movies": moviesPayload}).
                Post(fmt.Sprintf("https://api.trakt.tv/users/me/lists/%d/items", listID))
        }
        if len(showsPayload) > 0 {
            _, _ = client.R().
                SetBody(map[string]interface{}{"shows": showsPayload}).
                Post(fmt.Sprintf("https://api.trakt.tv/users/me/lists/%d/items", listID))
        }
        time.Sleep(time.Second)
    }
}

func openBrowser(url string) error {
    return exec.Command("open", url).Start()
}

