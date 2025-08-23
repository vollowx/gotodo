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
	"sort"
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

// Validation functions
func isValidPriority(p int8) bool {
	return p >= 1 && p <= 5
}

func isValidDeadline(t time.Time) bool {
	now := time.Now().Round(time.Hour * 24)
	return t.Compare(now) >= 0
}

// Conversion functions (parsing only, no validation)
func stringToPriority(s string) (*int8, error) {
	if s == "" {
		return nil, nil
	}
	p, err := strconv.ParseInt(s, 10, 8)
	if err != nil {
		return nil, errors.New("invalid priority format")
	}
	priority := int8(p)
	return &priority, nil
}

func stringToDeadline(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	dl, err := time.Parse(dateYYYYMMDD, s)
	if err != nil {
		return nil, fmt.Errorf("invalid deadline format, expected %s", dateYYYYMMDD)
	}
	return &dl, nil
}

func sortTodos(todos []Todo) []Todo {
	sorted := make([]Todo, len(todos))
	copy(sorted, todos)

	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		
		// done: todo > done
		if a.Done != b.Done {
			return !a.Done && b.Done
		}

		if a.Done && b.Done {
			// both done
			// doneAt: earlier > later
			return a.DoneAt.Before(b.DoneAt)
		}

		// both todo
		// deadline: earlier > later
		if !a.Deadline.Equal(b.Deadline) {
			return a.Deadline.Before(b.Deadline)
		}

		// priority: higher > lower
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}

		// addAt: earlier > later
		return a.AddedAt.Before(b.AddedAt)
	})

	return sorted
}


func patchTodos(todos []Todo, match string, patch TodoPatch) int {
	updated := 0
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
	return updated
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

func addTodoOperation(todos *[]Todo, mu *sync.Mutex, summary, details, deadlineStr, priorityStr string) (string, error) {
	if strings.TrimSpace(summary) == "" {
		return "", errors.New("summary is required")
	}

	deadline, err := stringToDeadline(deadlineStr)
	if err != nil {
		return "", err
	}
	dl := time.Now().Round(time.Hour * 24)
	if deadline != nil {
		if !isValidDeadline(*deadline) {
			return "", errors.New("deadline is before today")
		}
		dl = *deadline
	}

	priority, err := stringToPriority(priorityStr)
	if err != nil {
		return "", err
	}
	p := int8(1)
	if priority != nil {
		if !isValidPriority(*priority) {
			return "", errors.New("priority out of range (1-5)")
		}
		p = *priority
	}

	newTodo := Todo{
		Priority: p,
		Deadline: dl,
		AddedAt:  time.Now(),
		DoneAt:   time.Time{},
		Done:     false,
		Summary:  strings.TrimSpace(summary),
		Details:  details,
	}

	mu.Lock()
	*todos = append(*todos, newTodo)
	dump(*todos)
	mu.Unlock()

	return "todo added", nil
}

func deleteTodoOperation(todos *[]Todo, mu *sync.Mutex, summary string) string {
	mu.Lock()
	newTodos, removed := deleteTodos(*todos, summary)
	if removed > 0 {
		*todos = newTodos
		dump(*todos)
	}
	mu.Unlock()

	if removed == 0 {
		return fmt.Sprintf("no todo found with summary: %q", summary)
	}
	return fmt.Sprintf("deleted %d todo(s) with summary %q", removed, summary)
}

func setTodoOperation(todos *[]Todo, mu *sync.Mutex, match string, patch TodoPatch) (string, error) {
	if patch.Priority != nil {
		if !isValidPriority(*patch.Priority) {
			return "", errors.New("priority out of range (1-5)")
		}
	}

	if patch.Deadline != nil {
		if !isValidDeadline(*patch.Deadline) {
			return "", errors.New("deadline is before today")
		}
	}

	if patch.Summary != nil && strings.TrimSpace(*patch.Summary) == "" {
		return "", errors.New("summary cannot be empty")
	}

	mu.Lock()
	updated := patchTodos(*todos, match, patch)
	if updated > 0 {
		dump(*todos)
	}
	mu.Unlock()

	if updated == 0 {
		return fmt.Sprintf("no todo found with summary: %q", match), nil
	}
	return fmt.Sprintf("updated %d todo(s) with summary %q", updated, match), nil
}

func readLine(reader *bufio.Reader, prompt string) (string, error) {
	fmt.Print(prompt)
	s, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(s), nil
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

	if p, err := stringToPriority(params.Get("priority")); err != nil {
		return TodoPatch{}, err
	} else if p != nil {
		patch.Priority = p
	}

	if dl, err := stringToDeadline(params.Get("deadline")); err != nil {
		return TodoPatch{}, err
	} else if dl != nil {
		patch.Deadline = dl
	}

	return patch, nil
}

func bashCssValue(s string) string {
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
		"hashCssValue": bashCssValue,
	}
	t := template.New("base").Funcs(funcs)
	files := []string{"index.html", "edit.html"}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, filepath.Join("templates", f))
	}
	return t.ParseFiles(paths...)
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
				Todos:   append([]Todo(nil), sortTodos(todos)...),
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

		// APIs using abstracted operations

		http.HandleFunc("/api/add", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}

			message, err := addTodoOperation(&todos, &mu,
				r.Form.Get("summary"),
				r.Form.Get("details"),
				r.Form.Get("deadline"),
				r.Form.Get("priority"),
			)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			http.Redirect(w, r, "/?flash="+url.QueryEscape(message), http.StatusSeeOther)
		})

		http.HandleFunc("/api/delete/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			raw := strings.TrimPrefix(r.URL.Path, "/api/delete/")
			match, err := url.PathUnescape(raw)
			if err != nil {
				http.Error(w, "invalid summary", http.StatusBadRequest)
				return
			}

			message := deleteTodoOperation(&todos, &mu, match)

			http.Redirect(w, r, "/?flash="+url.QueryEscape(message), http.StatusSeeOther)
		})

		http.HandleFunc("/api/set/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			raw := strings.TrimPrefix(r.URL.Path, "/api/set/")
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

			message, err := setTodoOperation(&todos, &mu, match, patch)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			http.Redirect(w, r, "/?flash="+url.QueryEscape(message), http.StatusSeeOther)
		})

		log.Println("serving on http://localhost:8080")
		log.Fatal(http.ListenAndServe(":8080", nil))

	case args.Add != nil:
		reader := bufio.NewReader(os.Stdin)

		deadline, err := readLine(reader, "deadline: ")
		if err != nil {
			log.Println("add:", err)
			break
		}

		summary, err := readLine(reader, "summary: ")
		if err != nil {
			log.Println("add:", err)
			break
		}

		details, err := readLine(reader, "details: ")
		if err != nil {
			log.Println("add:", err)
			break
		}

		priority, err := readLine(reader, "priority (1-5, default 1): ")
		if err != nil {
			log.Println("add:", err)
			break
		}

		message, err := addTodoOperation(&todos, &mu, summary, details, deadline, priority)
		if err != nil {
			log.Println("add:", err)
		} else {
			fmt.Println(message)
		}

	case args.List != nil:
		for _, todo := range sortTodos(todos) {
			if args.List.All {
				todo.PrintAll()
			} else if todo.Done {
				continue
			} else {
				todo.Print()
			}
		}

	case args.Delete != nil:
		message := deleteTodoOperation(&todos, &mu, args.Delete.Summary)
		fmt.Println(message)

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
			dl, err := stringToDeadline(*args.Set.Deadline)
			if err != nil {
				log.Println("set:", err)
				break
			}
			patch.Deadline = dl
		}

		message, err := setTodoOperation(&todos, &mu, args.Set.Match, patch)
		if err != nil {
			log.Println("set:", err)
		} else {
			fmt.Println(message)
		}
	}
}
