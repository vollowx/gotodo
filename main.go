package main

import (
	"bufio"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alexflint/go-arg"
)

var homePath = os.Getenv("HOME")
var dataFile = fmt.Sprintf("%s/.gotodo.json", homePath)

const dateYYYYMMDD = "2006-01-02"

type Todo struct {
	Priority int8
	Deadline time.Time
	AddedAt  time.Time
	DoneAt   time.Time
	Done     bool
	Summary  string
	Details  string
}

type TodoPatch struct {
	Done     *bool
	Summary  *string
	Details  *string
	Priority *int8
	Deadline *time.Time
}

func (x Todo) Print() {
	fmt.Printf("[%d/%s] %s\n", x.Priority, x.Deadline.Format(dateYYYYMMDD), x.Summary)
}

func (x Todo) PrintAll() {
	var done string
	if x.Done {
		done = "done"
	} else {
		done = "todo"
	}
	fmt.Printf("[%s]     %s\n", done, x.Summary)
	fmt.Printf("details    %s\n", x.Details)
	fmt.Printf("added at   %s\n", x.AddedAt.Format(dateYYYYMMDD))
	fmt.Printf("deadline   %s\n", x.Deadline.Format(dateYYYYMMDD))
	fmt.Printf("priority   %d\n", x.Priority)
}

func parsePriority(s string) (*int8, error) {
	if s == "" {
		return nil, nil
	}
	p, err := strconv.Atoi(s)
	if err != nil {
		return nil, errors.New("invalid priority")
	}
	if p < 1 || p > 5 {
		return nil, errors.New("priority out of range (1-5)")
	}
	pp := int8(p)
	return &pp, nil
}

func parseDeadline(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	dl, err := time.Parse(dateYYYYMMDD, s)
	if err != nil {
		return nil, fmt.Errorf("invalid deadline format, expected %s", dateYYYYMMDD)
	}
	now := time.Now().Round(time.Hour * 24)
	if dl.Compare(now) == -1 {
		return nil, errors.New("deadline is before today")
	}
	return &dl, nil
}

func createTodo(summary, details, deadlineStr, priorityStr string) (Todo, error) {
	if strings.TrimSpace(summary) == "" {
		return Todo{}, errors.New("summary is required")
	}

	deadline, err := parseDeadline(deadlineStr)
	if err != nil {
		return Todo{}, err
	}
	if deadline == nil {
		return Todo{}, errors.New("deadline is required")
	}

	priority, err := parsePriority(priorityStr)
	if err != nil {
		return Todo{}, err
	}
	p := int8(1) // default
	if priority != nil {
		p = *priority
	}

	return Todo{
		Priority: p,
		Deadline: *deadline,
		AddedAt:  time.Now(),
		DoneAt:   time.Time{},
		Done:     false,
		Summary:  strings.TrimSpace(summary),
		Details:  details,
	}, nil
}

func patchTodos(todos []Todo, match string, patch TodoPatch) (updated int, err error) {
	if patch.Priority != nil {
		pStr := strconv.Itoa(int(*patch.Priority))
		if _, err := parsePriority(pStr); err != nil {
			return 0, err
		}
	}

	if patch.Deadline != nil {
		dlStr := patch.Deadline.Format(dateYYYYMMDD)
		if _, err := parseDeadline(dlStr); err != nil {
			return 0, err
		}
	}

	if patch.Summary != nil && strings.TrimSpace(*patch.Summary) == "" {
		return 0, errors.New("summary cannot be empty")
	}

	now := time.Now()
	for i := range todos {
		if todos[i].Summary != match {
			continue
		}
		if patch.Done != nil {
			if *patch.Done {
				if !todos[i].Done {
					todos[i].DoneAt = now
					todos[i].Done = true
				} else {
					todos[i].Done = false
					todos[i].DoneAt = time.Time{}
				}
			}
		}
		if patch.Summary != nil {
			todos[i].Summary = *patch.Summary
		}
		if patch.Details != nil {
			todos[i].Details = *patch.Details
		}
		if patch.Priority != nil {
			todos[i].Priority = *patch.Priority
		}
		if patch.Deadline != nil {
			todos[i].Deadline = *patch.Deadline
		}
		updated++
	}
	return updated, nil
}

func deleteTodos(todos []Todo, summary string) ([]Todo, int) {
	out := make([]Todo, 0, len(todos))
	deleted := 0
	for _, t := range todos {
		if t.Summary == summary {
			deleted++
			continue
		}
		out = append(out, t)
	}
	return out, deleted
}

func findFirstTodo(todos []Todo, summary string) (Todo, bool) {
	for _, t := range todos {
		if t.Summary == summary {
			return t, true
		}
	}
	return Todo{}, false
}

func dump(todos []Todo) {
	f, err := os.Create(dataFile)
	if err != nil {
		log.Println("create file:", err)
		return
	}
	defer f.Close()

	b, err := json.Marshal(todos)
	if err != nil {
		log.Println(err)
	}
	_, _ = f.Write(b)
}

func load(filename string) ([]Todo, error) {
	f, err := os.Open(filename)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Todo{}, nil
		}
		return nil, err
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	var todos []Todo
	if err := dec.Decode(&todos); err != nil {
		// If file is empty, treat as no todos
		if errors.Is(err, io.EOF) {
			return []Todo{}, nil
		}
		return nil, err
	}
	return todos, nil
}

// Web interface

func isTrue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "t", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func paramsToPatch(params url.Values) (TodoPatch, error) {
	var patch TodoPatch

	if isTrue(params.Get("done")) {
		t := true
		patch.Done = &t
	}

	if s := strings.TrimSpace(params.Get("summary")); s != "" {
		patch.Summary = &s
	}
	if d := params.Get("details"); d != "" {
		patch.Details = &d
	}

	if p, err := parsePriority(params.Get("priority")); err != nil {
		return TodoPatch{}, err
	} else if p != nil {
		patch.Priority = p
	}

	if dl, err := parseDeadline(params.Get("deadline")); err != nil {
		return TodoPatch{}, err
	} else if dl != nil {
		patch.Deadline = dl
	}

	return patch, nil
}

func cssValueEscape(s string) string {
	if s == "" {
		return "css-empty"
	}

	hash := md5.Sum([]byte(s))
	return "css-" + hex.EncodeToString(hash[:])
}

type IndexData struct {
	Today   string
	ShowAll bool
	Flash   string
	Todos   []Todo
}

type EditData struct {
	Match   string
	Example Todo
}

func loadTemplates() (*template.Template, error) {
	funcs := template.FuncMap{
		"fmtDate":     func(t time.Time) string { return t.Format(dateYYYYMMDD) },
		"pathEsc":     url.PathEscape,
		"qEsc":        url.QueryEscape,
		"htmlEsc":     html.EscapeString,
		"cssValueEsc": cssValueEscape,
	}
	t := template.New("base").Funcs(funcs)
	files := []string{"index.html", "edit.html"}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, filepath.Join("templates", f))
	}
	return t.ParseFiles(paths...)
}

func cliAddTodo() (Todo, error) {
	reader := bufio.NewReader(os.Stdin)

	readLine := func(prompt string) (string, error) {
		fmt.Print(prompt)
		s, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return strings.TrimSpace(s), nil
	}

	deadline, err := readLine("deadline: ")
	if err != nil {
		return Todo{}, err
	}

	summary, err := readLine("summary: ")
	if err != nil {
		return Todo{}, err
	}

	details, err := readLine("details: ")
	if err != nil {
		return Todo{}, err
	}

	priority, err := readLine("priority (1-5, default 1): ")
	if err != nil {
		return Todo{}, err
	}

	return createTodo(summary, details, deadline, priority)
}

func main() {
	type ServeCmd struct{}
	type AddCmd struct{}
	type ListCmd struct {
		All bool `arg:"-a" help:"list done todos"`
	}
	type DeleteCmd struct {
		Summary string `arg:"positional,required" help:"summary of the todo(s) to delete"`
	}
	type SetCmd struct {
		Match    string  `arg:"positional,required" help:"summary of the todo(s) to update"`
		Done     *bool   `arg:"--done" help:"toggle done status"`
		Summary  *string `arg:"--summary"`
		Details  *string `arg:"--details"`
		Priority *int8   `arg:"--priority"`
		Deadline *string `arg:"--deadline"`
	}

	var args struct {
		Serve  *ServeCmd  `arg:"subcommand:serve" help:"start local web server"`
		Add    *AddCmd    `arg:"subcommand:add" help:"add a new todo"`
		Delete *DeleteCmd `arg:"subcommand:delete" help:"delete todo(s) by summary"`
		Set    *SetCmd    `arg:"subcommand:set" help:"update properties of todo(s) by summary"`
		List   *ListCmd   `arg:"subcommand:list" help:"list todos"`
	}
	arg.MustParse(&args)

	todos, err := load(dataFile)
	if err != nil {
		log.Println("load", err)
	}
	var mu sync.Mutex

	switch {
	case args.Serve != nil:
		tmpl, err := loadTemplates()
		if err != nil {
			log.Fatalf("parse templates: %v", err)
		}

		// Interactive pages

		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			showAll := isTrue(r.URL.Query().Get("all"))
			flash := r.URL.Query().Get("flash")

			mu.Lock()
			data := IndexData{
				Today:   time.Now().Format(dateYYYYMMDD),
				ShowAll: showAll,
				Flash:   flash,
				Todos:   append([]Todo(nil), todos...),
			}
			mu.Unlock()

			if err := tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
				http.Error(w, "template error", http.StatusInternalServerError)
				log.Println("render index:", err)
			}
		})

		http.HandleFunc("/edit/", func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/edit/") {
				http.NotFound(w, r)
				return
			}
			raw := strings.TrimPrefix(r.URL.Path, "/edit/")
			match, err := url.PathUnescape(raw)
			if err != nil {
				http.Error(w, "invalid summary", http.StatusBadRequest)
				return
			}

			mu.Lock()
			example, ok := findFirstTodo(todos, match)
			mu.Unlock()
			if !ok {
				http.NotFound(w, r)
				return
			}

			data := EditData{
				Match:   match,
				Example: example,
			}
			if err := tmpl.ExecuteTemplate(w, "edit.html", data); err != nil {
				http.Error(w, "template error", http.StatusInternalServerError)
				log.Println("render edit:", err)
			}
		})

		// {{{ APIs

		http.HandleFunc("/add", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}

			newTodo, err := createTodo(
				r.Form.Get("summary"),
				r.Form.Get("details"),
				r.Form.Get("deadline"),
				r.Form.Get("priority"),
			)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			mu.Lock()
			todos = append(todos, newTodo)
			dump(todos)
			mu.Unlock()

			http.Redirect(w, r, "/?flash="+url.QueryEscape("todo added"), http.StatusSeeOther)
		})

		http.HandleFunc("/delete/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			raw := strings.TrimPrefix(r.URL.Path, "/delete/")
			match, err := url.PathUnescape(raw)
			if err != nil {
				http.Error(w, "invalid summary", http.StatusBadRequest)
				return
			}
			mu.Lock()
			newTodos, removed := deleteTodos(todos, match)
			if removed > 0 {
				todos = newTodos
				dump(todos)
			}
			mu.Unlock()

			flash := url.QueryEscape(fmt.Sprintf("deleted %d todo(s) with summary %q", removed, match))
			http.Redirect(w, r, "/?flash="+flash, http.StatusSeeOther)
		})

		http.HandleFunc("/set/", func(w http.ResponseWriter, r *http.Request) {
			raw := strings.TrimPrefix(r.URL.Path, "/set/")
			match, err := url.PathUnescape(raw)
			if err != nil {
				http.Error(w, "invalid summary", http.StatusBadRequest)
				return
			}

			_ = r.ParseForm()

			patch, err := paramsToPatch(r.Form)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			mu.Lock()
			updated, err := patchTodos(todos, match, patch)
			if err != nil {
				mu.Unlock()
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if updated > 0 {
				dump(todos)
			}
			mu.Unlock()

			flash := url.QueryEscape(fmt.Sprintf("updated %d todo(s) with summary %q", updated, match))
			http.Redirect(w, r, "/?flash="+flash, http.StatusSeeOther)
		})

		// }}}

		log.Println("serving on http://localhost:8080")
		log.Fatal(http.ListenAndServe(":8080", nil))

	case args.Add != nil:
		todo, err := cliAddTodo()
		if err != nil {
			log.Println("add:", err)
		} else {
			todos = append(todos, todo)
			fmt.Println("todo added")
			dump(todos)
		}

	case args.List != nil:
		for _, todo := range todos {
			if args.List.All {
				todo.PrintAll()
			} else if todo.Done {
				continue
			} else {
				todo.Print()
			}
		}

	case args.Delete != nil:
		new_todos, removed := deleteTodos(todos, args.Delete.Summary)
		if removed == 0 {
			fmt.Printf("no todo found with summary: %q\n", args.Delete.Summary)
		} else {
			fmt.Printf("deleted %d todo(s) with summary %q\n", removed, args.Delete.Summary)
			dump(new_todos)
		}

	case args.Set != nil:
		if args.Set.Done == nil && args.Set.Summary == nil && args.Set.Details == nil && args.Set.Priority == nil && args.Set.Deadline == nil {
			log.Println("set: no fields provided; use --done/--summary/--details/--priority/--deadline")
			break
		}

		patch := TodoPatch{
			Done:     args.Set.Done,
			Summary:  args.Set.Summary,
			Details:  args.Set.Details,
			Priority: args.Set.Priority,
		}

		if args.Set.Deadline != nil {
			dl, err := parseDeadline(*args.Set.Deadline)
			if err != nil {
				log.Println("set:", err)
				break
			}
			patch.Deadline = dl
		}

		updated, err := patchTodos(todos, args.Set.Match, patch)
		if err != nil {
			log.Println("set:", err)
			break
		}
		if updated == 0 {
			fmt.Printf("no todo found with summary: %q\n", args.Set.Match)
		} else {
			fmt.Printf("updated %d todo(s) with summary %q\n", updated, args.Set.Match)
			dump(todos)
		}
	}
}

// vim:ts=4:sw=4:sts=4
