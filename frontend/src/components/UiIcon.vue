<script setup lang="ts">
import type { Component } from 'vue'
import {
  Activity, Archive, AtSign, BarChart3, BookOpen, Check, ChevronLeft, ChevronRight, CircleAlert,
  ClipboardList, Code2, Copy, Database, FileText, Globe2, Inbox, KeyRound, LayoutDashboard,
  Mail, Menu, Moon, MoreHorizontal, PanelLeftClose, Plus, RefreshCw, Search, ServerCog, Settings,
  ShieldCheck, Sun, Trash2, UserRound, X, Zap, Languages, LogOut, PanelLeftOpen, Pencil, Play, Pause, Gift, Bell, BellOff, Wifi, WifiOff,
  Webhook, Send, MessageCircle, Cloud, Eye, EyeOff,
} from '@lucide/vue'

const icons: Record<string, Component> = {
  activity: Activity, archive: Archive, at: AtSign, chart: BarChart3, docs: BookOpen, book: BookOpen, check: Check,
  back: ChevronLeft, next: ChevronRight, alert: CircleAlert, clipboard: ClipboardList, code: Code2,
  copy: Copy, database: Database, file: FileText, domains: Globe2, inbox: Inbox, key: KeyRound,
  dashboard: LayoutDashboard, mail: Mail, menu: Menu, moon: Moon, more: MoreHorizontal,
  closePanel: PanelLeftClose, openPanel: PanelLeftOpen, plus: Plus, refresh: RefreshCw, search: Search, operations: ServerCog,
  settings: Settings, shield: ShieldCheck, sun: Sun, trash: Trash2, user: UserRound, close: X, zap: Zap,
  edit: Pencil, play: Play, pause: Pause,
  languages: Languages, logout: LogOut,
  gift: Gift,
  bell: Bell, bellOff: BellOff, wifi: Wifi, wifiOff: WifiOff,
  webhook: Webhook, send: Send, message: MessageCircle, cloud: Cloud,
  eye: Eye, eyeOff: EyeOff,
}

const props = withDefaults(defineProps<{ name: string; size?: number; strokeWidth?: number }>(), { size: 16, strokeWidth: 1.8 })
</script>

<template>
  <component :is="icons[props.name] || FileText" :size="props.size" :stroke-width="props.strokeWidth" aria-hidden="true" />
</template>
