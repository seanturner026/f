export type PickOption = { value: string; label: string }
// What the picker pane shows: code blocks to copy.
export type Pick = { kind: 'code'; options: PickOption[] }

declare module 'claude-code' {
  interface PluginState {
    f: {
      plan: Shaped<string | null>
      pick: Shaped<Pick | null>
    }
  }
}
