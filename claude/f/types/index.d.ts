export type PickOption = { value: string; label: string }
// What the picker pane shows: code blocks to copy, a feature to open, or repos for a new one.
export type Pick =
  | { kind: 'code'; options: PickOption[] }
  | { kind: 'feature'; options: PickOption[] }
  | { kind: 'repos'; name: string; options: PickOption[]; chosen: string[] }

declare module 'claude-code' {
  interface PluginState {
    f: {
      plan: Shaped<string | null>
      pick: Shaped<Pick | null>
    }
  }
}
