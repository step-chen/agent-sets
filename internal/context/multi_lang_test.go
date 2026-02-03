package context

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContextEngine_Analyze_Python(t *testing.T) {
	engine := NewContextEngine()
	src := `
import os
from flask import Flask

def my_func():
    print("hello")
    db.execute("SELECT * FROM users")

class MyClass:
    def method(self):
        pass
`
	analysis, err := engine.Analyze(context.Background(), "test.py", []byte(src))
	assert.NoError(t, err)

	// Check dependencies
	// "import os" -> os
	// "from flask" -> flask
	var deps []string
	for _, d := range analysis.Dependencies {
		deps = append(deps, d.Path)
	}
	assert.Contains(t, deps, "os")
	assert.Contains(t, deps, "flask")

	// Check chunks
	// my_func, MyClass, method (if captured separately depending on query?)
	// Query: (function_definition) @function, (class_definition) @class
	// Usually my_func and MyClass are top level. method is inside MyClass.
	// Tree-sitter query captures nested nodes too.
	var chunkNames []string
	for _, c := range analysis.Chunks {
		chunkNames = append(chunkNames, c.Name)
	}
	assert.Contains(t, chunkNames, "my_func")
	assert.Contains(t, chunkNames, "MyClass")
	// If method is captured
	// Note: method_definition is NOT in our pythonChunkQuery?
	// query: (function_definition) @function. In python methods are function_definition inside class.
	assert.Contains(t, chunkNames, "method")

	// Check SQL
	assert.Len(t, analysis.SQLReferences, 1)
	assert.Equal(t, "SELECT * FROM users", analysis.SQLReferences[0].Content)
}

func TestContextEngine_Analyze_Java(t *testing.T) {
	engine := NewContextEngine()
	src := `
import java.util.List;

class MyClass {
    void method() {
        db.query("INSERT INTO table (col) VALUES (1)");
    }
}
`
	analysis, err := engine.Analyze(context.Background(), "test.java", []byte(src))
	assert.NoError(t, err)

	// Check dependencies
	var deps []string
	for _, d := range analysis.Dependencies {
		deps = append(deps, d.Path)
	}
	assert.Contains(t, deps, "java.util.List")

	// Check chunks
	var chunkNames []string
	for _, c := range analysis.Chunks {
		chunkNames = append(chunkNames, c.Name)
	}
	assert.Contains(t, chunkNames, "MyClass")
	assert.Contains(t, chunkNames, "method") // "method"

	// Check SQL
	assert.Len(t, analysis.SQLReferences, 1)
	assert.Equal(t, "INSERT INTO table (col) VALUES (1)", analysis.SQLReferences[0].Content)
}
