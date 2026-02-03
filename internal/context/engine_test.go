package context

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContextEngine_Analyze_Go(t *testing.T) {
	engine := NewContextEngine()
	src := `
package main

import (
	"fmt"
	"net/http"
)

func main() {
	fmt.Println("Hello")
}
`
	analysis, err := engine.Analyze(context.Background(), "main.go", []byte(src))
	assert.NoError(t, err)
	assert.NotNil(t, analysis)
	assert.Equal(t, "main.go", analysis.Path)
	assert.Equal(t, ".go", analysis.Language)
	assert.Len(t, analysis.Dependencies, 2)

	// Map dependencies for easy checking
	deps := make(map[string]string)
	for _, d := range analysis.Dependencies {
		deps[d.Path] = d.Type
	}

	assert.Contains(t, deps, "fmt")
	assert.Equal(t, "import", deps["fmt"])
	assert.Contains(t, deps, "net/http")
}

func TestContextEngine_Analyze_Cpp(t *testing.T) {
	engine := NewContextEngine()
	src := `
#include <iostream>
#include "myheader.h"

int main() {
    return 0;
}
`
	analysis, err := engine.Analyze(context.Background(), "main.cpp", []byte(src))
	assert.NoError(t, err)
	assert.NotNil(t, analysis)
	assert.Len(t, analysis.Dependencies, 2)

	deps := make(map[string]string)
	for _, d := range analysis.Dependencies {
		deps[d.Path] = d.Type
	}

	assert.Contains(t, deps, "iostream")
	assert.Equal(t, "system_include", deps["iostream"])
	assert.Contains(t, deps, "myheader.h")
	assert.Equal(t, "include", deps["myheader.h"])
}

func TestContextEngine_Analyze_Go_Chunks(t *testing.T) {
	engine := NewContextEngine()
	src := `package main
func main() {
    println("hello")
}
func add(a, b int) int {
    return a + b
}
type MyStruct struct{}
func (s *MyStruct) Method() {}
`
	analysis, err := engine.Analyze(context.Background(), "main.go", []byte(src))
	assert.NoError(t, err)

	// We expect 3 chunks: main, add, Method
	// Note: MyStruct is not a function/method, so not chunked by current query
	assert.Len(t, analysis.Chunks, 3)

	names := make([]string, 0)
	for _, c := range analysis.Chunks {
		names = append(names, c.Name)
	}
	assert.Contains(t, names, "main")
	assert.Contains(t, names, "add")
	assert.Contains(t, names, "Method")
}

func TestContextEngine_ExtractSQL(t *testing.T) {
	engine := NewContextEngine()
	src := `package main
func main() {
	db.Query("SELECT * FROM users")
	db.Exec("INSERT INTO users VALUES (1)")
	loader.Load("queries.sql")
	fmt.Println("SELECT * FROM ignored") // Should be ignored (func name not relevant)
}
`
	analysis, err := engine.Analyze(context.Background(), "main.go", []byte(src))
	assert.NoError(t, err)

	assert.Len(t, analysis.SQLReferences, 3)

	refs := analysis.SQLReferences
	// Order depends on traversal. Usually sequential.
	assert.Equal(t, "embedded", refs[0].Type)
	assert.Equal(t, "SELECT * FROM users", refs[0].Content)

	assert.Equal(t, "embedded", refs[1].Type)
	assert.Equal(t, "INSERT INTO users VALUES (1)", refs[1].Content)

	assert.Equal(t, "file_ref", refs[2].Type)
	assert.Equal(t, "queries.sql", refs[2].Content)
}
