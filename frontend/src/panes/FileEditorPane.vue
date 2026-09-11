<script setup>
import { ref } from 'vue'
import PaneHeader from '@/components/layout/PaneHeader.vue'

const content = ref(`package main

import (
	"context"
	"fmt"
)

// App struct
type App struct {
	ctx context.Context
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// Greet returns a greeting for the given name
func (a *App) Greet(name string) string {
	return fmt.Sprintf("Hello %s, It's show time!", name)
}
`)

const modified = ref(false)

function onInput() {
  modified.value = true
}

function save() {
  modified.value = false
}
</script>

<template>
  <div class="file-editor-pane">
    <PaneHeader type="file-editor" title="app.go">
      <template #extra>
        <span v-if="modified" class="modified-dot">●</span>
        <button class="btn btn-ghost btn-sm" :disabled="!modified" @click="save">保存</button>
      </template>
    </PaneHeader>
    <div class="editor-body">
      <div class="line-numbers">
        <div v-for="n in content.split('\n').length" :key="n">{{ n }}</div>
      </div>
      <textarea
        class="editor-textarea"
        v-model="content"
        @input="onInput"
        spellcheck="false"
      />
    </div>
  </div>
</template>

<style scoped lang="scss">
.file-editor-pane {
  display: flex;
  flex-direction: column;
  height: 100%;
  background-color: $color-bg-primary;
}

.editor-body {
  display: flex;
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

.line-numbers {
  padding: $space-md $space-sm;
  background-color: $color-bg-secondary;
  border-right: 1px solid $color-border;
  text-align: right;
  font-family: $font-family-mono;
  font-size: $font-size-xs;
  color: $color-text-muted;
  line-height: $line-height-sm;
  user-select: none;
  overflow-y: hidden;
}

.line-numbers div {
  height: $line-height-sm;
}

.editor-textarea {
  flex: 1;
  padding: $space-md;
  background: transparent;
  border: none;
  outline: none;
  resize: none;
  font-family: $font-family-mono;
  font-size: $font-size-xs;
  line-height: $line-height-sm;
  color: $color-text-primary;
  tab-size: 2;
}

.modified-dot {
  color: $color-warning;
}

.btn-sm {
  padding: 2px $space-sm;
  font-size: $font-size-xs;
}
</style>
