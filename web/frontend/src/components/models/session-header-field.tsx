import { Field } from "@/components/shared-form"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

export const sessionHeaderPreset = "x-opencode-session"

/**
 * Session header input with a one-click preset. The preset value lives here
 * (not in the translations) so translating the button label can never change
 * the header name that gets written to the config.
 */
export function SessionHeaderField({
  label,
  hint,
  value,
  onChange,
}: {
  label: string
  hint: string
  value: string
  onChange: (value: string) => void
}) {
  return (
    <Field label={label} hint={hint}>
      <div className="flex items-center gap-2">
        <Input
          value={value}
          onChange={(event) => onChange(event.target.value)}
          placeholder={sessionHeaderPreset}
          className="flex-1"
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={value === sessionHeaderPreset}
          onClick={() => onChange(sessionHeaderPreset)}
        >
          {sessionHeaderPreset}
        </Button>
      </div>
    </Field>
  )
}
