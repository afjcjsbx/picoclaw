import { useTranslation } from "react-i18next"

import type { SubagentLimits, SubagentToolSettings } from "@/api/subagents"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"

interface SubagentToolsCardProps {
  tools: SubagentToolSettings
  onChange: (patch: Partial<SubagentToolSettings>) => void
}

export function SubagentToolsCard({ tools, onChange }: SubagentToolsCardProps) {
  const { t } = useTranslation()

  return (
    <Card className="shadow-none">
      <CardHeader>
        <CardTitle>
          {t("pages.agent.subagents.tools.title", "Subagent tools")}
        </CardTitle>
        <CardDescription>
          {t(
            "pages.agent.subagents.tools.description",
            "Control how agents delegate work to sub-turns and peer agents.",
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-5">
        <ToolSwitchRow
          id="subagent-tool-runtime"
          checked={tools.subagent}
          label={t("pages.agent.subagents.tools.subagent", "Subagent runtime")}
          description={t(
            "pages.agent.subagents.tools.subagent_hint",
            "Shared runtime used by spawn, subagent, and spawn_status.",
          )}
          onCheckedChange={(checked) => onChange({ subagent: checked })}
        />
        <ToolSwitchRow
          id="subagent-tool-spawn"
          checked={tools.spawn}
          label={t("pages.agent.subagents.tools.spawn", "Background spawn")}
          description={t(
            "pages.agent.subagents.tools.spawn_hint",
            "Let an agent launch a background sub-turn and keep working.",
          )}
          onCheckedChange={(checked) => onChange({ spawn: checked })}
        />
        <ToolSwitchRow
          id="subagent-tool-spawn-status"
          checked={tools.spawn_status}
          label={t("pages.agent.subagents.tools.spawn_status", "Spawn status")}
          description={t(
            "pages.agent.subagents.tools.spawn_status_hint",
            "Expose the tool that lists and inspects active subagent tasks.",
          )}
          onCheckedChange={(checked) => onChange({ spawn_status: checked })}
        />
      </CardContent>
    </Card>
  )
}

function ToolSwitchRow({
  id,
  checked,
  label,
  description,
  onCheckedChange,
}: {
  id: string
  checked: boolean
  label: string
  description: string
  onCheckedChange: (checked: boolean) => void
}) {
  return (
    <div className="flex items-start justify-between gap-4">
      <div className="min-w-0 space-y-1">
        <Label htmlFor={id} className="cursor-pointer text-sm font-medium">
          {label}
        </Label>
        <p className="text-muted-foreground text-[13px] leading-relaxed">
          {description}
        </p>
      </div>
      <Switch
        id={id}
        checked={checked}
        onCheckedChange={onCheckedChange}
        className="mt-0.5 shrink-0"
      />
    </div>
  )
}

interface SubagentLimitsCardProps {
  limits: SubagentLimits
  onChange: (patch: Partial<SubagentLimits>) => void
}

export function SubagentLimitsCard({
  limits,
  onChange,
}: SubagentLimitsCardProps) {
  const { t } = useTranslation()

  return (
    <Card className="shadow-none">
      <CardHeader>
        <CardTitle>
          {t("pages.agent.subagents.limits.title", "Runtime limits")}
        </CardTitle>
        <CardDescription>
          {t(
            "pages.agent.subagents.limits.description",
            "Global bounds applied to every sub-turn. Zero keeps the built-in default.",
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className="grid gap-5 sm:grid-cols-2">
        <LimitField
          id="subagent-limit-depth"
          label={t("pages.agent.subagents.limits.max_depth", "Max depth")}
          hint={t("pages.agent.subagents.limits.max_depth_hint", "Default: 3")}
          value={limits.max_depth}
          min={0}
          onChange={(value) => onChange({ max_depth: value })}
        />
        <LimitField
          id="subagent-limit-concurrent"
          label={t(
            "pages.agent.subagents.limits.max_concurrent",
            "Max concurrent",
          )}
          hint={t(
            "pages.agent.subagents.limits.max_concurrent_hint",
            "Default: 3, capped at 32",
          )}
          value={limits.max_concurrent}
          min={0}
          max={32}
          onChange={(value) => onChange({ max_concurrent: value })}
        />
        <LimitField
          id="subagent-limit-concurrency-timeout"
          label={t(
            "pages.agent.subagents.limits.concurrency_timeout_sec",
            "Concurrency wait (seconds)",
          )}
          hint={t(
            "pages.agent.subagents.limits.concurrency_timeout_hint",
            "Default: 30",
          )}
          value={limits.concurrency_timeout_sec}
          min={0}
          onChange={(value) => onChange({ concurrency_timeout_sec: value })}
        />
        <LimitField
          id="subagent-limit-default-timeout"
          label={t(
            "pages.agent.subagents.limits.default_timeout_minutes",
            "Task timeout (minutes)",
          )}
          hint={t(
            "pages.agent.subagents.limits.default_timeout_hint",
            "Default: 5",
          )}
          value={limits.default_timeout_minutes}
          min={0}
          onChange={(value) => onChange({ default_timeout_minutes: value })}
        />
        <LimitField
          id="subagent-limit-token-budget"
          label={t(
            "pages.agent.subagents.limits.default_token_budget",
            "Token budget",
          )}
          hint={t(
            "pages.agent.subagents.limits.default_token_budget_hint",
            "0 = no sub-turn token budget",
          )}
          value={limits.default_token_budget}
          min={0}
          onChange={(value) => onChange({ default_token_budget: value })}
        />
      </CardContent>
    </Card>
  )
}

function LimitField({
  id,
  label,
  hint,
  value,
  min,
  max,
  onChange,
}: {
  id: string
  label: string
  hint: string
  value: number
  min: number
  max?: number
  onChange: (value: number) => void
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id} className="text-sm font-medium">
        {label}
      </Label>
      <Input
        id={id}
        type="number"
        min={min}
        max={max}
        value={value}
        onChange={(event) => {
          const next = Number.parseInt(event.target.value, 10)
          const parsed = Number.isNaN(next) ? min : next
          onChange(
            max === undefined
              ? Math.max(min, parsed)
              : Math.min(max, Math.max(min, parsed)),
          )
        }}
        className="h-10 rounded-xl"
      />
      <p className="text-muted-foreground text-[12px]">{hint}</p>
    </div>
  )
}
