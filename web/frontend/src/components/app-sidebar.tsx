import { IconChevronRight } from "@tabler/icons-react"
import {
  IconAtom,
  IconChevronsDown,
  IconChevronsUp,
  IconKey,
  IconListDetails,
  IconMessageCircle,
  IconPlus,
  IconSearch,
  IconSettings,
  IconSparkles,
  IconTools,
  IconTrash,
} from "@tabler/icons-react"
import { Link, useRouterState } from "@tanstack/react-router"
import { useAtom, useSetAtom } from "jotai"
import * as React from "react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuAction,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
  SidebarMenuSubButton,
  SidebarMenuSubItem,
  SidebarRail,
  useSidebar,
} from "@/components/ui/sidebar"
import {
  mergeSplitConversations,
  removeSplitConversation,
} from "@/features/chat/split-groups"
import { usePicoChat } from "@/hooks/use-pico-chat"
import { useSessionHistory } from "@/hooks/use-session-history"
import { useSidebarChannels } from "@/hooks/use-sidebar-channels"
import { sessionTitlesAtom, splitConversationsAtom } from "@/store/chat"

interface NavItem {
  title: string
  url: string
  icon: React.ComponentType<{ className?: string }>
  translateTitle?: boolean
}

interface NavGroup {
  label: string
  defaultOpen: boolean
  items: NavItem[]
  isChannelsGroup?: boolean
}

const baseNavGroups: Omit<NavGroup, "items">[] = [
  {
    label: "navigation.chat",
    defaultOpen: true,
  },
  {
    label: "navigation.model_group",
    defaultOpen: true,
  },
  {
    label: "navigation.agent_group",
    defaultOpen: true,
  },
  {
    label: "navigation.services",
    defaultOpen: true,
  },
]

const SESSION_DRAG_TYPE = "application/x-picoclaw-session"

function splitGroupKey(group: string[]) {
  return [...group].sort().join("\u0000")
}

export function AppSidebar({ ...props }: React.ComponentProps<typeof Sidebar>) {
  const routerState = useRouterState()
  const { i18n, t } = useTranslation()
  const { isMobile, setOpenMobile, state: sidebarState } = useSidebar()
  const currentPath = routerState.location.pathname
  const { activeSessionId, messages, newChat, switchSession } = usePicoChat()
  const [splitGroups, setSplitGroups] = useAtom(splitConversationsAtom)
  const setSessionTitles = useSetAtom(sessionTitlesAtom)
  const [expandedSplitId, setExpandedSplitId] = React.useState<string | null>(
    null,
  )
  const {
    sessions,
    hasMore,
    isLoadingMore,
    loadError,
    loadErrorMessage,
    observerRef,
    loadSessions,
    handleDeleteSession,
  } = useSessionHistory({
    activeSessionId,
    onDeletedActiveSession: newChat,
  })
  const latestMessageId = messages[messages.length - 1]?.id

  React.useEffect(() => {
    setSessionTitles(
      Object.fromEntries(sessions.map(({ id, title }) => [id, title])),
    )
  }, [sessions, setSessionTitles])

  const sessionById = React.useMemo(
    () => new Map(sessions.map((session) => [session.id, session])),
    [sessions],
  )
  const visibleSplitGroups = React.useMemo(
    () =>
      splitGroups
        .map((group) => ({
          ids: group,
          sessions: group
            .map((id) => sessionById.get(id))
            .filter((session) => session !== undefined),
        }))
        .filter((group) => group.sessions.length > 0),
    [sessionById, splitGroups],
  )
  const splitGroupBySession = React.useMemo(() => {
    const lookup = new Map<string, (typeof visibleSplitGroups)[number]>()
    for (const group of visibleSplitGroups) {
      for (const session of group.sessions) lookup.set(session.id, group)
    }
    return lookup
  }, [visibleSplitGroups])

  React.useEffect(() => {
    const group = splitGroups.find((item) => item.includes(activeSessionId))
    if (group) setExpandedSplitId(splitGroupKey(group))
  }, [activeSessionId, splitGroups])

  const handleSessionDrop = (
    event: React.DragEvent<HTMLButtonElement | HTMLAnchorElement>,
    targetId: string,
  ) => {
    if (!event.dataTransfer.types.includes(SESSION_DRAG_TYPE)) return
    event.preventDefault()
    const sourceId = event.dataTransfer.getData(SESSION_DRAG_TYPE)
    if (!sourceId) return

    const nextGroups = mergeSplitConversations(splitGroups, sourceId, targetId)
    if (nextGroups === splitGroups) return
    setSplitGroups(nextGroups)
    const group = nextGroups.find((item) => item.includes(targetId))
    if (group) setExpandedSplitId(splitGroupKey(group))
    void switchSession(targetId)
  }

  const handleSessionDragOver = (
    event: React.DragEvent<HTMLButtonElement | HTMLAnchorElement>,
  ) => {
    if (!event.dataTransfer.types.includes(SESSION_DRAG_TYPE)) return
    event.preventDefault()
    event.dataTransfer.dropEffect = "move"
  }

  const renderSessionLink = (session: (typeof sessions)[number]) => (
    <Link
      to="/"
      onClick={() => {
        if (session.id !== activeSessionId) void switchSession(session.id)
        handleNavItemClick()
      }}
      draggable
      onDragStart={(event) => {
        event.dataTransfer.setData(SESSION_DRAG_TYPE, session.id)
        event.dataTransfer.effectAllowed = "move"
      }}
      onDragOver={handleSessionDragOver}
      onDrop={(event) => handleSessionDrop(event, session.id)}
    >
      <IconMessageCircle className="size-4 opacity-70" />
      <span>{session.title}</span>
    </Link>
  )

  React.useEffect(() => {
    if (sidebarState === "expanded") {
      void loadSessions(true)
    }
  }, [activeSessionId, latestMessageId, loadSessions, sidebarState])

  const {
    channelItems,
    hasMoreChannels,
    showAllChannels,
    toggleShowAllChannels,
  } = useSidebarChannels({
    language: (i18n.resolvedLanguage ?? i18n.language ?? "").toLowerCase(),
    t,
  })

  const handleNavItemClick = React.useCallback(() => {
    if (isMobile) {
      setOpenMobile(false)
    }
  }, [isMobile, setOpenMobile])

  const navGroups: NavGroup[] = React.useMemo(() => {
    return [
      {
        ...baseNavGroups[0],
        items: [
          {
            title: "navigation.chat",
            url: "/",
            icon: IconMessageCircle,
            translateTitle: true,
          },
        ],
      },
      {
        ...baseNavGroups[1],
        items: [
          {
            title: "navigation.models",
            url: "/models",
            icon: IconAtom,
            translateTitle: true,
          },
          {
            title: "navigation.credentials",
            url: "/credentials",
            icon: IconKey,
            translateTitle: true,
          },
        ],
      },
      {
        label: "navigation.channels_group",
        defaultOpen: true,
        items: channelItems.map((item) => ({
          title: item.title,
          url: item.url,
          icon: item.icon,
          translateTitle: false,
        })),
        isChannelsGroup: true,
      },
      {
        ...baseNavGroups[2],
        items: [
          {
            title: "navigation.hub",
            url: "/agent/hub",
            icon: IconSearch,
            translateTitle: true,
          },
          {
            title: "navigation.skills",
            url: "/agent/skills",
            icon: IconSparkles,
            translateTitle: true,
          },
          {
            title: "navigation.tools",
            url: "/agent/tools",
            icon: IconTools,
            translateTitle: true,
          },
        ],
      },
      {
        ...baseNavGroups[3],
        items: [
          {
            title: "navigation.config",
            url: "/config",
            icon: IconSettings,
            translateTitle: true,
          },
          {
            title: "navigation.logs",
            url: "/logs",
            icon: IconListDetails,
            translateTitle: true,
          },
        ],
      },
    ]
  }, [channelItems])

  return (
    <Sidebar
      {...props}
      className="border-sidebar-border bg-sidebar border-r pt-3"
    >
      <SidebarContent className="bg-sidebar">
        <div className="px-4 pb-3">
          <Button
            asChild
            variant="outline"
            className="bg-background h-10 w-full justify-start gap-2.5 rounded-xl shadow-none"
          >
            <Link
              to="/"
              onClick={() => {
                newChat()
                handleNavItemClick()
              }}
            >
              <IconPlus className="size-4" />
              {t("chat.newChat")}
            </Link>
          </Button>
        </div>
        {sidebarState === "expanded" && (
          <SidebarGroup className="px-2 py-0">
            <SidebarGroupLabel className="px-2">
              {t("chat.history")}
            </SidebarGroupLabel>
            <SidebarGroupContent className="pt-1">
              <div className="max-h-[min(42vh,24rem)] overflow-y-auto">
                <SidebarMenu>
                  {sessions.length > 0 ? (
                    sessions.map((session) =>
                      (() => {
                        const splitGroup = splitGroupBySession.get(session.id)
                        if (splitGroup) {
                          if (splitGroup.sessions[0]?.id !== session.id) {
                            return null
                          }

                          const groupKey = splitGroupKey(splitGroup.ids)
                          const groupTitle =
                            (splitGroup.ids.includes(activeSessionId)
                              ? sessionById.get(activeSessionId)?.title
                              : undefined) ?? splitGroup.sessions[0].title

                          return (
                            <SidebarMenuItem key={groupKey}>
                              <Collapsible
                                open={expandedSplitId === groupKey}
                                onOpenChange={(open) =>
                                  setExpandedSplitId(open ? groupKey : null)
                                }
                                className="group/collapsible"
                              >
                                <CollapsibleTrigger asChild>
                                  <SidebarMenuButton
                                    type="button"
                                    isActive={splitGroup.ids.includes(
                                      activeSessionId,
                                    )}
                                    tooltip={groupTitle}
                                    draggable
                                    onDragStart={(event) => {
                                      event.dataTransfer.setData(
                                        SESSION_DRAG_TYPE,
                                        session.id,
                                      )
                                      event.dataTransfer.effectAllowed = "move"
                                    }}
                                    onDragOver={handleSessionDragOver}
                                    onDrop={(event) =>
                                      handleSessionDrop(event, session.id)
                                    }
                                    className="text-muted-foreground hover:text-foreground h-9 rounded-lg px-3"
                                  >
                                    <IconChevronRight className="size-3.5 transition-transform group-data-[state=open]/collapsible:rotate-90" />
                                    <span>{groupTitle}</span>
                                    <span className="text-muted-foreground ml-auto text-xs">
                                      {splitGroup.ids.length}
                                    </span>
                                  </SidebarMenuButton>
                                </CollapsibleTrigger>
                                <CollapsibleContent>
                                  <SidebarMenuSub>
                                    {splitGroup.sessions.map((member) => (
                                      <SidebarMenuSubItem key={member.id}>
                                        <SidebarMenuSubButton
                                          asChild
                                          isActive={
                                            member.id === activeSessionId
                                          }
                                          className="pr-8"
                                        >
                                          {renderSessionLink(member)}
                                        </SidebarMenuSubButton>
                                        <button
                                          type="button"
                                          aria-label={t("chat.deleteSession")}
                                          title={t("chat.deleteSession")}
                                          onClick={() =>
                                            void (async () => {
                                              if (
                                                await handleDeleteSession(
                                                  member.id,
                                                )
                                              ) {
                                                setSplitGroups((groups) =>
                                                  removeSplitConversation(
                                                    groups,
                                                    member.id,
                                                  ),
                                                )
                                              }
                                            })()
                                          }
                                          className="text-muted-foreground hover:text-destructive absolute top-1 right-1 rounded p-1 opacity-0 group-hover/menu-sub-item:opacity-100 focus-visible:opacity-100"
                                        >
                                          <IconTrash className="size-3.5" />
                                        </button>
                                      </SidebarMenuSubItem>
                                    ))}
                                  </SidebarMenuSub>
                                </CollapsibleContent>
                              </Collapsible>
                            </SidebarMenuItem>
                          )
                        }

                        if (splitGroupBySession.has(session.id)) return null

                        return (
                          <SidebarMenuItem key={session.id}>
                            <SidebarMenuButton
                              asChild
                              isActive={session.id === activeSessionId}
                              tooltip={session.title}
                              className="text-muted-foreground hover:text-foreground h-9 rounded-lg px-3"
                            >
                              {renderSessionLink(session)}
                            </SidebarMenuButton>
                            <SidebarMenuAction
                              showOnHover
                              aria-label={t("chat.deleteSession")}
                              onClick={() =>
                                void (async () => {
                                  if (await handleDeleteSession(session.id)) {
                                    setSplitGroups((groups) =>
                                      removeSplitConversation(
                                        groups,
                                        session.id,
                                      ),
                                    )
                                  }
                                })()
                              }
                            >
                              <IconTrash />
                            </SidebarMenuAction>
                          </SidebarMenuItem>
                        )
                      })(),
                    )
                  ) : (
                    <SidebarMenuItem>
                      <div className="text-muted-foreground px-3 py-2 text-xs">
                        {loadError
                          ? loadErrorMessage
                          : isLoadingMore
                            ? t("chat.loadingMore")
                            : t("chat.noHistory")}
                      </div>
                    </SidebarMenuItem>
                  )}
                  {hasMore && sessions.length > 0 && (
                    <SidebarMenuItem>
                      <div
                        ref={observerRef}
                        className="text-muted-foreground min-h-2 px-3 py-1 text-center text-xs"
                      >
                        {isLoadingMore ? t("chat.loadingMore") : null}
                      </div>
                    </SidebarMenuItem>
                  )}
                </SidebarMenu>
              </div>
            </SidebarGroupContent>
          </SidebarGroup>
        )}
        {navGroups.map((group) => (
          <Collapsible
            key={group.label}
            defaultOpen={group.defaultOpen}
            className="group/collapsible mb-1"
          >
            <SidebarGroup className="px-2 py-0">
              <SidebarGroupLabel asChild>
                <CollapsibleTrigger className="text-muted-foreground/80 hover:text-foreground hover:bg-sidebar-accent/70 flex w-full cursor-pointer items-center justify-between rounded-md px-2 py-1.5 text-[11px] font-semibold tracking-wide uppercase transition-colors">
                  <span>{t(group.label)}</span>
                  <IconChevronRight className="size-3.5 opacity-50 transition-transform duration-200 group-data-[state=open]/collapsible:rotate-90" />
                </CollapsibleTrigger>
              </SidebarGroupLabel>
              <CollapsibleContent>
                <SidebarGroupContent className="pt-1">
                  <SidebarMenu>
                    {group.items.map((item) => {
                      const isActive =
                        currentPath === item.url ||
                        (item.url !== "/" &&
                          currentPath.startsWith(`${item.url}/`))
                      return (
                        <SidebarMenuItem key={item.title}>
                          <SidebarMenuButton
                            asChild
                            isActive={isActive}
                            onClick={handleNavItemClick}
                            data-tour={
                              item.url === "/models" ? "models-nav" : undefined
                            }
                            className={`h-9 rounded-lg px-3 ${isActive ? "bg-sidebar-accent text-sidebar-accent-foreground font-medium" : "text-muted-foreground hover:bg-sidebar-accent/70 hover:text-foreground"}`}
                          >
                            <Link to={item.url}>
                              <item.icon
                                className={`size-4 ${isActive ? "opacity-100" : "opacity-60"}`}
                              />
                              <span
                                className={
                                  isActive ? "opacity-100" : "opacity-80"
                                }
                              >
                                {item.translateTitle === false
                                  ? item.title
                                  : t(item.title)}
                              </span>
                            </Link>
                          </SidebarMenuButton>
                        </SidebarMenuItem>
                      )
                    })}
                    {group.isChannelsGroup && hasMoreChannels && (
                      <SidebarMenuItem key="channels-more-toggle">
                        <SidebarMenuButton
                          onClick={toggleShowAllChannels}
                          className="text-muted-foreground hover:bg-muted/60 h-9 px-3"
                        >
                          {showAllChannels ? (
                            <IconChevronsUp className="size-4 opacity-60" />
                          ) : (
                            <IconChevronsDown className="size-4 opacity-60" />
                          )}
                          <span className="opacity-80">
                            {showAllChannels
                              ? t("navigation.show_less_channels")
                              : t("navigation.show_more_channels")}
                          </span>
                        </SidebarMenuButton>
                      </SidebarMenuItem>
                    )}
                  </SidebarMenu>
                </SidebarGroupContent>
              </CollapsibleContent>
            </SidebarGroup>
          </Collapsible>
        ))}
      </SidebarContent>
      <SidebarRail />
    </Sidebar>
  )
}
