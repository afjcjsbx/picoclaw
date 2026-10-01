import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

const LOG_LEVEL_OPTIONS = ["debug", "info", "warn", "error", "fatal"] as const
type GatewayLogLevel = (typeof LOG_LEVEL_OPTIONS)[number]

const LOG_LEVEL_LABELS: Record<GatewayLogLevel, string> = {
  debug: "Debug",
  info: "Info",
  warn: "Warn",
  error: "Error",
  fatal: "Fatal",
}

type LogLevelSelectProps = {
  value: GatewayLogLevel
  onValueChange: (value: GatewayLogLevel) => void
}

export function LogLevelSelect({ value, onValueChange }: LogLevelSelectProps) {
  return (
    <div className="flex items-center gap-2">
      <Select
        value={value}
        onValueChange={(nextValue) =>
          onValueChange(nextValue as GatewayLogLevel)
        }
      >
        <SelectTrigger size="sm" className="w-28">
          <SelectValue />
        </SelectTrigger>
        <SelectContent align="end">
          {LOG_LEVEL_OPTIONS.map((level) => (
            <SelectItem key={level} value={level}>
              {LOG_LEVEL_LABELS[level]}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}

export type { GatewayLogLevel }
