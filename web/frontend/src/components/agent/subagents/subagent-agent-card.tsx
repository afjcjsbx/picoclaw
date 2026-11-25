import { IconFolder, IconRobot, IconUsers } from "@tabler/icons-react"
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"

import type { SubagentAgent } from "@/api/subagents"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"

const INHERIT_MODEL = "__inherit__"

interface SubagentAgentCardProps {
  agent: SubagentAgent
  agents: SubagentAgent[]
  models: string[]
  configured: boolean
  onChange: (updater: (agent: SubagentAgent) => SubagentAgent) => void
}

export function SubagentAgentCard({
  agent,
  agents,
  models,
  configured,
  onChange,
}: SubagentAgentCardProps) {
  const { t } = useTranslation()
  const peers = agents.filter((peer) => peer.id !== agent.id)
  const allowAll = agent.allow_agents.includes("*")
  const allowedCount = allowAll
    ? peers.length
    : agent.allow_agents.filter((id) => id !== "*").length
  const modelValue = agent.subagent_model?.primary ?? INHERIT_MODEL
  const modelOptions = Array.from(
    new Set([
      ...models,
      ...(agent.subagent_model?.primary ? [agent.subagent_model.primary] : []),
    ]),
  )

  const togglePeer = (peerID: string, checked: boolean) => {
    onChange((current) => {
      const allowed = new Set(current.allow_agents.filter((id) => id !== "*"))
      if (checked) {
        allowed.add(peerID)
      } else {
        allowed.delete(peerID)
      }
      return { ...current, allow_agents: Array.from(allowed) }
    })
  }

  const updateModel = (value: string) => {
    onChange((current) => {
      if (value === INHERIT_MODEL) {
        return { ...current, subagent_model: undefined }
      }
      return {
        ...current,
        subagent_model: {
          primary: value,
          fallbacks: current.subagent_model?.fallbacks ?? [],
        },
      }
    })
  }

  const updateFallbacks = (value: string) => {
    const fallbacks = value
      .split(",")
      .map((item) => item.trim())
      .filter(Boolean)
    onChange((current) =>
      current.subagent_model
        ? {
            ...current,
            subagent_model: {
              ...current.subagent_model,
              fallbacks,
            },
          }
        : current,
    )
  }

  return (
    <Card className="shadow-none">
      <CardHeader className="border-border/40 border-b">
        <div className="flex items-start gap-3">
          <div className="bg-muted/60 text-muted-foreground flex size-10 shrink-0 items-center justify-center rounded-xl">
            <IconRobot className="size-5" />
          </div>
          <div className="min-w-0 flex-1 space-y-1">
            <div className="flex flex-wrap items-center gap-2">
              <CardTitle className="min-w-0 text-base">
                {agent.name || agent.id}
              </CardTitle>
              {agent.default && (
                <Badge variant="secondary" className="text-[10px]">
                  {t("pages.agent.subagents.agents.default_badge", "Default")}
                </Badge>
              )}
              {agent.implicit && (
                <Badge variant="outline" className="text-[10px]">
                  {t("pages.agent.subagents.agents.implicit_badge", "Implicit")}
                </Badge>
              )}
            </div>
            <div className="text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1 text-[12px]">
              <span className="font-mono">{agent.id}</span>
              <span className="inline-flex min-w-0 items-center gap-1">
                <IconFolder className="size-3.5 shrink-0" />
                <span className="max-w-72 truncate" title={agent.workspace}>
                  {agent.workspace}
                </span>
              </span>
              {agent.model && (
                <span className="inline-flex items-center gap-1">
                  <IconRobot className="size-3.5 shrink-0" />
                  <span className="font-mono">{agent.model}</span>
                </span>
              )}
            </div>
          </div>
        </div>
      </CardHeader>

      <CardContent className="space-y-6 pt-5">
        <section className="space-y-3">
          <div className="flex items-start justify-between gap-3">
            <div className="space-y-0.5">
              <Label className="text-sm font-medium">
                {t(
                  "pages.agent.subagents.agents.allowed_title",
                  "Allowed peers",
                )}
              </Label>
              <p className="text-muted-foreground text-[12px]">
                {t(
                  "pages.agent.subagents.agents.allowed_hint",
                  "Peers this agent may target with spawn or delegate.",
                )}
              </p>
            </div>
            <Badge variant="outline" className="shrink-0 text-[10px]">
              {t("pages.agent.subagents.agents.peer_count", {
                count: allowedCount,
                defaultValue: "{{count}} allowed",
              })}
            </Badge>
          </div>

          {peers.length === 0 ? (
            <p className="text-muted-foreground rounded-lg border border-dashed px-3 py-2.5 text-[13px]">
              {t(
                "pages.agent.subagents.agents.no_peers",
                "No other agents are configured.",
              )}
            </p>
          ) : (
            <div className="space-y-2">
              <PeerToggle
                id={`subagent-${agent.id}-all`}
                label={t(
                  "pages.agent.subagents.agents.all_allowed",
                  "All agents",
                )}
                description={t(
                  "pages.agent.subagents.agents.all_allowed_hint",
                  "Allow this agent to target every configured peer.",
                )}
                checked={allowAll}
                disabled={!configured}
                onCheckedChange={(checked) =>
                  onChange((current) => ({
                    ...current,
                    allow_agents: checked ? ["*"] : [],
                  }))
                }
              />
              {peers.map((peer) => (
                <PeerToggle
                  key={peer.id}
                  id={`subagent-${agent.id}-${peer.id}`}
                  label={peer.name || peer.id}
                  description={peer.id}
                  checked={allowAll || agent.allow_agents.includes(peer.id)}
                  disabled={!configured || allowAll}
                  onCheckedChange={(checked) => togglePeer(peer.id, checked)}
                />
              ))}
            </div>
          )}
        </section>

        <section className="border-border/40 space-y-3 border-t pt-5">
          <div className="space-y-0.5">
            <Label
              htmlFor={`subagent-model-${agent.id}`}
              className="text-sm font-medium"
            >
              {t(
                "pages.agent.subagents.agents.subagent_model",
                "Subagent model override",
              )}
            </Label>
            <p className="text-muted-foreground text-[12px]">
              {t(
                "pages.agent.subagents.agents.subagent_model_hint",
                "Use a different model when this agent runs as a subagent.",
              )}
            </p>
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <Select
              value={modelValue}
              disabled={!configured}
              onValueChange={updateModel}
            >
              <SelectTrigger
                id={`subagent-model-${agent.id}`}
                className="h-10 w-full rounded-xl"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={INHERIT_MODEL}>
                  {t(
                    "pages.agent.subagents.agents.inherit_model",
                    "Inherit agent model",
                  )}
                </SelectItem>
                {modelOptions.map((model) => (
                  <SelectItem key={model} value={model}>
                    {model}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <FallbackModelsInput
              value={agent.subagent_model?.fallbacks ?? []}
              disabled={!configured || modelValue === INHERIT_MODEL}
              placeholder={t(
                "pages.agent.subagents.agents.fallbacks_placeholder",
                "Fallback models, comma separated",
              )}
              onChange={updateFallbacks}
            />
          </div>
        </section>

        {!configured && (
          <p className="text-muted-foreground flex items-center gap-2 text-[12px]">
            <IconUsers className="size-3.5 shrink-0" />
            {t(
              "pages.agent.subagents.agents.implicit_hint",
              "Add agents.list to the configuration to manage peer permissions.",
            )}
          </p>
        )}
      </CardContent>
    </Card>
  )
}

function PeerToggle({
  id,
  label,
  description,
  checked,
  disabled,
  onCheckedChange,
}: {
  id: string
  label: string
  description: string
  checked: boolean
  disabled: boolean
  onCheckedChange: (checked: boolean) => void
}) {
  return (
    <div className="bg-muted/30 flex items-center justify-between gap-4 rounded-xl px-3 py-2.5">
      <div className="min-w-0">
        <Label htmlFor={id} className="cursor-pointer text-[13px] font-medium">
          {label}
        </Label>
        <p className="text-muted-foreground truncate font-mono text-[11px]">
          {description}
        </p>
      </div>
      <Switch
        id={id}
        checked={checked}
        disabled={disabled}
        onCheckedChange={onCheckedChange}
      />
    </div>
  )
}

function FallbackModelsInput({
  value,
  disabled,
  placeholder,
  onChange,
}: {
  value: string[]
  disabled: boolean
  placeholder: string
  onChange: (value: string) => void
}) {
  const [text, setText] = useState(value.join(", "))

  useEffect(() => {
    setText(value.join(", "))
  }, [value])

  return (
    <Input
      value={text}
      disabled={disabled}
      placeholder={placeholder}
      onChange={(event) => setText(event.target.value)}
      onBlur={() => onChange(text)}
      onKeyDown={(event) => {
        if (event.key === "Enter") {
          event.currentTarget.blur()
        }
      }}
      className="h-10 rounded-xl"
    />
  )
}
