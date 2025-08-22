package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/alexflint/go-arg"
)

var homePath = os.Getenv("HOME")
var dataFile = fmt.Sprintf("%s/.gotodo.json", homePath)

const shortForm = "2006-01-02"

type Todo struct {
	Priority int8
	Deadline time.Time
	AddedAt  time.Time
	DoneAt   time.Time
	Done     bool
	Summary  string
	Details  string
}

type TodoDiff struct {
	Done     *bool
	Summary  *string
	Details  *string
	Priority *int8
	Deadline *time.Time
}

func (x Todo) Print() {
	fmt.Printf("@%d        %s\n", x.Priority, x.Summary)
	fmt.Printf("details   %s\n", x.Details)
	fmt.Printf("deadline  %s\n", x.Deadline.Format(shortForm))
}

func newTodo() (Todo, error) {
	reader := bufio.NewReader(os.Stdin)

	readLine := func(prompt string) (string, error) {
		fmt.Print(prompt)
		s, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return strings.TrimSpace(s), nil
	}

	var (
		_deadline string
		summary   string
		details   string
	)

	var err error
	if _deadline, err = readLine("deadline: "); err != nil {
		return Todo{}, err
	}
	now := time.Now()
	deadline, perr := time.Parse(shortForm, _deadline)
	if perr != nil {
		return Todo{}, fmt.Errorf("invalid deadline format, expected %s", shortForm)
	}
	if deadline.Compare(now) == -1 {
		return Todo{}, errors.New("deadline is before today")
	}

	if summary, err = readLine("summary: "); err != nil {
		return Todo{}, err
	}
	if details, err = readLine("details: "); err != nil {
		return Todo{}, err
	}

	priority := int8(1) // default
	if pstr, err := readLine("priority (1-5, default 1): "); err == nil && pstr != "" {
		p, convErr := strconv.Atoi(pstr)
		if convErr != nil {
			return Todo{}, errors.New("invalid priority")
		}
		priority = int8(p)
		if priority > 5 || priority < 1 {
			return Todo{}, errors.New("priority out of range")
		}
	}

	todo := Todo{}

	todo.AddedAt = time.Now()
	todo.Deadline = deadline
	todo.Summary = summary
	todo.Details = details
	todo.Priority = priority

	return todo, nil
}

func deleteBySummary(todos []Todo, summary string) ([]Todo, int) {
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

func updateBySummary(todos []Todo, match string, patch TodoDiff) (updated int) {
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

func main() {
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

	switch {
	case args.Add != nil:
		todo, err := newTodo()
		if err != nil {
			log.Println("new:", err)
		} else {
			todos = append(todos, todo)
			fmt.Println("todo added")
			dump(todos)
		}
	case args.List != nil:
		for index, todo := range todos {
			if args.List.All {
				if todo.Done {
					fmt.Printf("[%d] DONE\n", index)
				} else {
					fmt.Printf("[%d] TODO\n", index)
				}
			} else {
				if todo.Done {
					continue
				}
				fmt.Printf("[%d]\n", index)
			}
			todo.Print()
		}
	case args.Delete != nil:
		new_todos, removed := deleteBySummary(todos, args.Delete.Summary)
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

		if args.Set.Priority != nil {
			if *args.Set.Priority < 1 || *args.Set.Priority > 5 {
				log.Println("set: priority out of range (1-5)")
				break
			}
		}

		patch := TodoDiff{
			Done:     args.Set.Done,
			Summary:  args.Set.Summary,
			Details:  args.Set.Details,
			Priority: args.Set.Priority,
		}

		if args.Set.Deadline != nil {
			dl, err := time.Parse(shortForm, *args.Set.Deadline)
			if err != nil {
				log.Printf("set: invalid deadline format, expected %s\n", shortForm)
				break
			}
			now := time.Now()
			if dl.Compare(now) == -1 {
				log.Println("set: deadline is before today")
				break
			}
			patch.Deadline = &dl
		}

		updated := updateBySummary(todos, args.Set.Match, patch)
		if updated == 0 {
			fmt.Printf("no todo found with summary: %q\n", args.Set.Match)
		} else {
			fmt.Printf("updated %d todo(s) with summary %q\n", updated, args.Set.Match)
			dump(todos)
		}
	}
}
