package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/zeus-kim/vssh/internal/fleet"
)

func memoryUsage() {
	fmt.Fprintln(os.Stderr, `usage:
  vssh memory get [node]
  vssh memory set <node> [--role=R] [--services=a,b] [--tags=x,y]
  vssh memory note <node> <text>
  vssh memory find [--role=R] [--tag=T] [--service=S] [query]
  vssh memory auto-note <node> [output]   (reads stdin if output omitted)
  vssh memory ask <query>
  vssh memory discover [nodes...] [--apply]   auto-detect role/services/tags from
                                              what each node actually runs`)
}

func cmdMemory(args []string) {
	if len(args) == 0 {
		memoryUsage()
		os.Exit(1)
	}
	switch args[0] {
	case "get":
		cmdMemoryGet(args[1:])
	case "set":
		cmdMemorySet(args[1:])
	case "note":
		cmdMemoryNote(args[1:])
	case "find":
		cmdMemoryFind(args[1:])
	case "auto-note", "autonote":
		cmdMemoryAutoNote(args[1:])
	case "ask":
		cmdMemoryAsk(args[1:])
	case "discover":
		cmdMemoryDiscover(args[1:])
	default:
		memoryUsage()
		os.Exit(1)
	}
}

func loadMemoryOrExit() *fleet.FleetMemory {
	fm, err := fleet.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "vssh: %v\n", err)
		os.Exit(1)
	}
	return fm
}

func printJSON(v interface{}) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

func cmdMemoryGet(args []string) {
	fm := loadMemoryOrExit()
	if len(args) == 0 {
		printJSON(fm)
		return
	}
	node := args[0]
	mem, ok := fm.GetNode(node)
	if !ok {
		fmt.Fprintf(os.Stderr, "vssh: no memory for node %q\n", node)
		os.Exit(1)
	}
	printJSON(mem)
}

func cmdMemorySet(args []string) {
	if len(args) == 0 {
		memoryUsage()
		os.Exit(1)
	}
	node := args[0]
	fm := loadMemoryOrExit()
	mem, _ := fm.GetNode(node)
	for _, a := range args[1:] {
		switch {
		case strings.HasPrefix(a, "--role="):
			mem.Role = strings.TrimPrefix(a, "--role=")
		case strings.HasPrefix(a, "--services="):
			mem.Services = splitCSV(strings.TrimPrefix(a, "--services="))
		case strings.HasPrefix(a, "--tags="):
			mem.Tags = splitCSV(strings.TrimPrefix(a, "--tags="))
		default:
			fmt.Fprintf(os.Stderr, "vssh: unknown flag %q\n", a)
			memoryUsage()
			os.Exit(1)
		}
	}
	fm.SetNode(node, mem)
	if err := fm.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "vssh: %v\n", err)
		os.Exit(1)
	}
	saved, _ := fm.GetNode(node)
	printJSON(saved)
}

func cmdMemoryNote(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: vssh memory note <node> <text>")
		os.Exit(1)
	}
	node := args[0]
	text := strings.TrimSpace(strings.Join(args[1:], " "))
	if text == "" {
		fmt.Fprintln(os.Stderr, "vssh: note text is required")
		os.Exit(1)
	}
	fm := loadMemoryOrExit()
	fm.AddNote(node, text)
	if err := fm.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "vssh: %v\n", err)
		os.Exit(1)
	}
	mem, _ := fm.GetNode(node)
	printJSON(mem)
}

func cmdMemoryFind(args []string) {
	var f fleet.FleetFilter
	var text []string
	// Index-based so each filter accepts both "--role gpu" and "--role=gpu",
	// matching `vssh intent`/`workflow`/`diff`.
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := func(name string) string {
			if strings.HasPrefix(a, name+"=") {
				return strings.TrimPrefix(a, name+"=")
			}
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "vssh: %s needs a value\n", name)
				os.Exit(1)
			}
			i++
			return args[i]
		}
		switch {
		case a == "--role" || strings.HasPrefix(a, "--role="):
			f.Role = value("--role")
		case a == "--tag" || strings.HasPrefix(a, "--tag="):
			f.Tag = value("--tag")
		case a == "--service" || strings.HasPrefix(a, "--service="):
			f.Service = value("--service")
		case strings.HasPrefix(a, "--"):
			fmt.Fprintf(os.Stderr, "vssh: unknown flag %q\n", a)
			memoryUsage()
			os.Exit(1)
		default:
			text = append(text, a)
		}
	}
	f.Text = strings.Join(text, " ")
	fm := loadMemoryOrExit()
	printJSON(fm.Find(f))
}

func cmdMemoryAutoNote(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: vssh memory auto-note <node> [output]  (reads stdin if output omitted)")
		os.Exit(1)
	}
	node := args[0]
	command := ""
	var rest []string
	for _, a := range args[1:] {
		if strings.HasPrefix(a, "--command=") {
			command = strings.TrimPrefix(a, "--command=")
			continue
		}
		rest = append(rest, a)
	}
	output := strings.Join(rest, " ")
	if strings.TrimSpace(output) == "" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "vssh: %v\n", err)
			os.Exit(1)
		}
		output = string(data)
	}
	if strings.TrimSpace(output) == "" {
		fmt.Fprintln(os.Stderr, "vssh: no output to analyze (pass as arg or via stdin)")
		os.Exit(1)
	}
	fm := loadMemoryOrExit()
	extracted := fm.AutoNote(node, command, output)
	if err := fm.Save(); err != nil {
		fmt.Fprintf(os.Stderr, "vssh: %v\n", err)
		os.Exit(1)
	}
	printJSON(map[string]interface{}{"node": node, "extracted": extracted})
}

func cmdMemoryAsk(args []string) {
	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		fmt.Fprintln(os.Stderr, "usage: vssh memory ask <query>")
		os.Exit(1)
	}
	fm := loadMemoryOrExit()
	hits := fm.Ask(query)
	if len(hits) == 0 {
		fmt.Printf("no matching nodes for %q\n", query)
		return
	}
	for _, h := range hits {
		role := h.Memory.Role
		if role == "" {
			role = "-"
		}
		fmt.Printf("%s (role=%s, score=%d)\n", h.Node, role, h.Score)
		for _, r := range h.Reasons {
			fmt.Printf("  - %s\n", r)
		}
	}
}

// splitCSV splits a comma list, trims each element, and drops empties.
func splitCSV(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
