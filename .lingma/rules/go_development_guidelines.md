---
trigger: always_on
name: development_guidelines
---

# Go Development Guidelines

## Overview

This document outlines the development guidelines and coding standards for the FluxAgent project. These rules aim to maintain code quality, consistency, and readability throughout the codebase.

## Code Style and Structure

### Comments
- Comments MUST be written in English
- Keep comments concise and meaningful
- Remove redundant or obvious comments
- Focus on explaining why rather than what
- Document exported functions, types, and variables with clear explanations

### Code Simplicity
- Write clean, elegant, and readable code
- Follow idiomatic Go practices and conventions
- Avoid over-engineering solutions
- Maintain consistent style throughout the codebase
- Remove unused code and unnecessary complexity

### Package Management
- Package names should be lowercase, single-word names (no underscores or mixedCase)
- Organize code logically into packages with clear responsibilities
- Avoid circular dependencies between packages
- Use standard Go project layout (cmd/, internal/, pkg/, configs/, etc.)

### Error Handling
- Always handle errors explicitly; never ignore them with `_`
- Use the project's unified error handling system based on `AppError` type
- Wrap errors with context using `errors.Wrap()` or `errors.Wrapf()` functions
- Use the `ErrorBuilder` pattern for creating new errors with metadata
- Leverage automatic logging capabilities with `WithAutoLog()` instead of manual logging
- Classify errors with appropriate error codes from the standard set
- Add contextual information using `WithContext()` for better debugging
- Use `DefaultErrorHandler` for consistent error processing and transformation
- Apply structured logging with zap fields for better observability

### Dependencies
- Use Go modules for dependency management
- Keep dependencies up-to-date and minimize their number
- Vet third-party packages for security and maintenance
- Use official libraries when available

### Concurrency
- Use goroutines and channels appropriately for concurrent operations
- Protect shared resources with mutexes or other synchronization primitives
- Avoid race conditions and deadlocks
- Limit the number of concurrent operations to prevent resource exhaustion
- Use context for cancellation and timeouts

### Performance
- Profile code to identify bottlenecks
- Minimize memory allocations where possible
- Use appropriate data structures for the task
- Close resources properly with defer statements
- Use buffered channels for better performance when appropriate

### Security
- Validate and sanitize all inputs
- Handle sensitive data (API keys, passwords) securely
- Use environment variables or secure configuration for secrets
- Follow principle of least privilege for file and network access