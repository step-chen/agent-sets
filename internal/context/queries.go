package context

// Tree-sitter queries for dependency extraction

const goDependencyQuery = `
(import_spec path: (interpreted_string_literal) @path)
`

const cppDependencyQuery = `
(preproc_include path: (string_literal) @path)
(preproc_include path: (system_lib_string) @path)
`

const goChunkQuery = `
(function_declaration) @function
(method_declaration) @method
`

const cppChunkQuery = `
(function_definition) @function
`

const pythonDependencyQuery = `
(import_statement name: (dotted_name) @path)
(import_from_statement module_name: (dotted_name) @path)
`

const javaDependencyQuery = `
(import_declaration (scoped_identifier) @path)
`

const pythonChunkQuery = `
(function_definition) @function
(class_definition) @class
`

const javaChunkQuery = `
(method_declaration) @method
(constructor_declaration) @method
(class_declaration) @class
`

const goCallQuery = `(call_expression) @call`
const cppCallQuery = `(call_expression) @call`
const pythonCallQuery = `(call) @call`
const javaCallQuery = `(method_invocation) @call`
