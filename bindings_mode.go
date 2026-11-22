//go:build bindings

package main

// bindingsGeneration is true for the temporary binary Wails builds with the
// "bindings" tag to regenerate frontend/wailsjs. That binary must always reach
// wails.Run, so startup shortcuts such as the single-instance probe are skipped.
const bindingsGeneration = true
