package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/alexflint/go-arg"
)

const dataFile = "gotodo.json"
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

func (x Todo) Print() {
	fmt.Printf("@%d        %s\n", x.Priority, x.Summary)
	fmt.Printf("details   %s\n", x.Details)
	fmt.Printf("deadline  %s\n", x.Deadline.Format(shortForm))
}

func newTodo() (Todo, error) {
	var _deadline, summary, details string
	var priority int8
	fmt.Printf("deadline: ")
	fmt.Scan(&_deadline)
	fmt.Printf("summary: ")
	fmt.Scan(&summary)
	fmt.Printf("details: ")
	fmt.Scan(&details)
	fmt.Printf("priority (1-5): ")
	fmt.Scan(&priority)

	var now = time.Now()
	var deadline, _ = time.Parse(shortForm, _deadline)

	todo := Todo{}

	if deadline.Compare(now) == -1 {
		return todo, errors.New("deadline is before today")
	}
	if priority > 5 || priority < 1 {
		return todo, errors.New("priority out of range")
	}

	todo.AddedAt = time.Now()
	todo.Deadline = deadline
	todo.Summary = summary
	todo.Details = details
	todo.Priority = priority

	return todo, nil
}

func dump(todos []Todo) {
	f, err := os.Create("gotodo.json")
	if err != nil {
		log.Println("create file:", err)
		return
	}
	defer f.Close()

	b, err := json.Marshal(todos)
	if err != nil {
		log.Println(err)
	}
	f.Write(b)
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
		All bool `arg:"-a"`
	}
	var args struct {
		Add  *AddCmd  `arg:"subcommand:add"`
		List *ListCmd `arg:"subcommand:list"`
	}
	arg.MustParse(&args)

	todos, err := load(dataFile)

	if err != nil {
		log.Println(err)
	}

	switch {
	case args.Add != nil:
		todo, err := newTodo()
		if err != nil {
			log.Println("new todo:", err)
		} else {
			todos = append(todos, todo)
		}
	case args.List != nil:
		for index, todo := range todos {
			if args.List.All {
				if todo.Done {
					fmt.Printf("[%d] DONE\n", index)
				} else {
					fmt.Printf("[%d] TODO\n", index)
				}
				todo.Print()
			} else {
				if todo.Done {
					continue
				}
				fmt.Printf("[%d]\n", index)
				todo.Print()
			}
		}
	}

	dump(todos)
}
