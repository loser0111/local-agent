<script setup>
import { ref } from 'vue'
import PaneHeader from '@/components/layout/PaneHeader.vue'

const terminalRef = ref(null)
const command = ref('')
const output = ref([
  { type: 'cmd', text: '$ npm run dev' },
  { type: 'out', text: '> local-agent@0.0.0 dev' },
  { type: 'out', text: '> vite' },
  { type: 'out', text: '' },
  { type: 'out', text: '  VITE v7.0.0  ready in 320 ms' },
  { type: 'out', text: '' },
  { type: 'out', text: '  ➜  Local:   http://localhost:5173/' },
  { type: 'out', text: '  ➜  Network: use --host to expose' },
  { type: 'out', text: '' },
])

function runCommand() {
  if (!command.value.trim()) return
  output.value.push({ type: 'cmd', text: `$ ${command.value}` })
  output.value.push({ type: 'out', text: `(模拟执行: ${command.value})` })
  command.value = ''
}

function handleKeydown(e) {
  if (e.key === 'Enter') {
    runCommand()
  }
}
</script>

<template>
  <div class="terminal-pane">
    <PaneHeader type="terminal" />
    <div class="terminal-body" ref="terminalRef">
      <div class="terminal-output">
        <div
          v-for="(line, i) in output"
          :key="i"
          :class="['terminal-line', line.type]"
        >{{ line.text }}</div>
      </div>
      <div class="terminal-input-line">
        <span class="prompt">$</span>
        <input
          v-model="command"
          class="terminal-input"
          @keydown="handleKeydown"
          autofocus
        />
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.terminal-pane {
  display: flex;
  flex-direction: column;
  height: 100%;
  background-color: #0d0d14;
}

.terminal-body {
  flex: 1;
  padding: $space-sm $space-md;
  font-family: $font-family-mono;
  font-size: $font-size-xs;
  overflow-y: auto;
}

.terminal-line {
  white-space: pre-wrap;
  line-height: $line-height-sm;
}

.terminal-line.cmd {
  color: $color-success;
}

.terminal-line.out {
  color: $color-text-secondary;
}

.terminal-input-line {
  display: flex;
  align-items: center;
  gap: $space-sm;
  margin-top: $space-sm;
}

.prompt {
  color: $color-success;
}

.terminal-input {
  flex: 1;
  background: transparent;
  border: none;
  outline: none;
  color: $color-text-primary;
  font-family: inherit;
  font-size: inherit;
}
</style>
