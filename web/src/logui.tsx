import { useMemo, type ReactNode } from 'react'
import { Card, DatePicker, Empty, Select, Segmented, Space, Table, Tag, Typography } from 'antd'
import dayjs, { type Dayjs } from 'dayjs'
import type { Device, LogColumn, LogWidget } from './api'
import { faDateTime, faNum } from './format'
import { brand } from './theme'
import { Mono, bytesText } from './ui'
import { useFetch } from './useFetch'

// --- values ---

export const severityFa: Record<string, [string, string]> = {
  critical: ['بحرانی', 'red'], high: ['زیاد', 'volcano'], medium: ['متوسط', 'orange'], low: ['کم', 'gold'],
  info: ['اطلاعات', 'blue'], information: ['اطلاعات', 'blue'], notice: ['اطلاع', 'blue'], warning: ['هشدار', 'orange'],
  error: ['خطا', 'red'], alert: ['هشدار فوری', 'red'], emergency: ['اضطراری', 'red'], debug: ['اشکال‌زدایی', 'default'],
}

const blocked = new Set(['deny', 'blocked', 'block', 'dropped', 'reset', 'clear_session', 'reset-client', 'reset-server', 'quarantine', 'alert_deny'])

export function durationText(sec: number): string {
  if (!sec) return '—'
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  if (h > 0) return `${faNum(h)} ساعت ${faNum(m)} دقیقه`
  if (m > 0) return `${faNum(m)} دقیقه`
  return `${faNum(sec)} ثانیه`
}

// cell renders one widget or log value by its column type.
export function cell(type: LogColumn['type'], v: unknown, devices?: Map<number, string>): ReactNode {
  if (v === null || v === undefined || v === '') return <Typography.Text type="secondary">—</Typography.Text>
  switch (type) {
    case 'bytes':
      return <span dir="ltr">{bytesText(Number(v))}</span>
    case 'number':
      return faNum(Number(v))
    case 'duration':
      return durationText(Number(v))
    case 'ip':
      return <Mono>{String(v)}</Mono>
    case 'severity': {
      const s = severityFa[String(v).toLowerCase()]
      return s ? <Tag color={s[1]}>{s[0]}</Tag> : <Tag>{String(v)}</Tag>
    }
    case 'action':
      return <Tag color={blocked.has(String(v).toLowerCase()) ? 'red' : 'green'}>{String(v)}</Tag>
    case 'device':
      return devices?.get(Number(v)) ?? String(v)
  }
  return <span style={{ unicodeBidi: 'plaintext' }}>{String(v)}</span>
}

// --- period and devices ---

export type Period = { preset: string; from: string; to: string }

const presets: Record<string, number> = { '1h': 1, '24h': 24, '7d': 24 * 7, '30d': 24 * 30 }

export function periodOf(preset: string, from?: string, to?: string): Period {
  if (preset === 'custom' && from && to) return { preset, from, to }
  const hours = presets[preset] ?? 24
  const now = dayjs()
  return { preset, from: now.subtract(hours, 'hour').toISOString(), to: now.toISOString() }
}

export function PeriodPicker({ value, onChange, options = ['1h', '24h', '7d', '30d'] }: { value: Period; onChange: (p: Period) => void; options?: string[] }) {
  const labels: Record<string, string> = { '1h': '۱ ساعت', '24h': '۲۴ ساعت', '7d': '۷ روز', '30d': '۳۰ روز', custom: 'بازه‌ی دلخواه' }
  return (
    <Space wrap>
      <Segmented
        value={value.preset}
        options={[...options, 'custom'].map((o) => ({ value: o, label: labels[o] }))}
        onChange={(v) => onChange(v === 'custom' ? { ...value, preset: 'custom' } : periodOf(String(v)))}
      />
      {value.preset === 'custom' && (
        <DatePicker.RangePicker
          showTime={{ format: 'HH:mm' }}
          format="YYYY-MM-DD HH:mm"
          value={[dayjs(value.from), dayjs(value.to)]}
          onChange={(r) => {
            const [a, b] = (r ?? []) as [Dayjs | null, Dayjs | null]
            if (a && b) onChange({ preset: 'custom', from: a.toISOString(), to: b.toISOString() })
          }}
        />
      )}
    </Space>
  )
}

export function useDevices() {
  const { data } = useFetch<{ items: Device[] }>('/devices')
  const list = data?.items ?? []
  const names = useMemo(() => new Map(list.map((d) => [d.id, d.name])), [list])
  return { list, names }
}

export function DeviceSelect({ value, onChange, devices, style }: { value?: number[]; onChange?: (v: number[]) => void; devices: Device[]; style?: React.CSSProperties }) {
  return (
    <Select
      mode="multiple"
      allowClear
      placeholder="همه‌ی دستگاه‌ها"
      value={value}
      onChange={onChange}
      style={{ minWidth: 220, ...style }}
      maxTagCount="responsive"
      optionFilterProp="label"
      options={devices.map((d) => ({ value: d.id, label: d.name }))}
    />
  )
}

export function rangeQuery(p: Period, devices: number[]): string {
  const q = new URLSearchParams({ from: p.from, to: p.to })
  if (devices.length) q.set('devices', devices.join(','))
  return q.toString()
}

// --- widgets ---

const colors = [brand.primary, '#6B7280', '#D97706']

function SeriesChart({ w }: { w: LogWidget }) {
  const rows = w.rows ?? []
  const W = 640
  const H = 190
  const pad = { l: 8, r: 8, t: 10, b: 24 }
  if (rows.length < 2) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="داده‌ای در این بازه نیست" />
  const n = rows.length
  const x = (i: number) => pad.l + (i * (W - pad.l - pad.r)) / (n - 1)
  const series = w.columns.map((c, si) => {
    const vals = rows.map((r) => Number(r[c.key] ?? 0))
    const max = Math.max(...vals, 0)
    return { c, vals, max, color: colors[si % colors.length] }
  })
  const span = new Date(String(rows[n - 1].t)).getTime() - new Date(String(rows[0].t)).getTime()
  const label = (i: number) => {
    const t = String(rows[i].t)
    return span <= 48 * 3600_000 ? new Intl.DateTimeFormat('fa-IR', { hour: '2-digit', minute: '2-digit' }).format(new Date(t)) : faDateTime(t).split(' ')[0]
  }
  return (
    <div>
      <svg viewBox={`0 0 ${W} ${H}`} style={{ width: '100%', height: 'auto', direction: 'ltr' }} role="img">
        {[0, 1, 2, 3, 4].map((i) => {
          const y = pad.t + ((H - pad.t - pad.b) * i) / 4
          return <line key={i} x1={pad.l} x2={W - pad.r} y1={y} y2={y} stroke="#EEE" />
        })}
        {series.map((s) => {
          const scale = s.max || 1
          const pts = s.vals.map((v, i) => `${x(i)},${pad.t + (1 - v / scale) * (H - pad.t - pad.b)}`)
          return (
            <g key={s.c.key}>
              <polygon points={`${x(0)},${H - pad.b} ${pts.join(' ')} ${x(n - 1)},${H - pad.b}`} fill={s.color} opacity={0.08} />
              <polyline points={pts.join(' ')} fill="none" stroke={s.color} strokeWidth={2} strokeLinejoin="round" />
            </g>
          )
        })}
        {[0, Math.floor(n / 2), n - 1].map((i) => (
          <text key={i} x={x(i)} y={H - 6} fontSize="10" fill="#888" textAnchor={i === 0 ? 'start' : i === n - 1 ? 'end' : 'middle'}>{label(i)}</text>
        ))}
      </svg>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 16, fontSize: 12 }}>
        {series.map((s) => (
          <span key={s.c.key}>
            <span style={{ display: 'inline-block', width: 10, height: 10, borderRadius: 2, background: s.color, marginInlineEnd: 6 }} />
            {s.c.title} — بیشینه: {s.c.type === 'bytes' ? <span dir="ltr">{bytesText(s.max)}</span> : faNum(s.max)}
          </span>
        ))}
      </div>
    </div>
  )
}

function Bars({ w, onDrill }: { w: LogWidget; onDrill?: (row: Record<string, string | number>) => void }) {
  const rows = w.rows ?? []
  if (!rows.length) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="داده‌ای در این بازه نیست" />
  const max = Math.max(...rows.map((r) => Number(r.value)), 1)
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {rows.map((r, i) => (
        <div key={i} onClick={onDrill ? () => onDrill(r) : undefined} style={{ display: 'flex', alignItems: 'center', gap: 10, cursor: onDrill ? 'pointer' : undefined }}>
          <span style={{ width: 150, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', unicodeBidi: 'plaintext' }} title={String(r.name)}>{String(r.name)}</span>
          <span style={{ flex: 1, height: 8, background: '#F0F0F0', borderRadius: 4 }}>
            <span style={{ display: 'block', height: 8, width: `${(Number(r.value) / max) * 100}%`, background: brand.primary, borderRadius: 4 }} />
          </span>
          <span style={{ width: 90, textAlign: 'left' }}>{cell(w.columns[1].type, r.value)}</span>
        </div>
      ))}
    </div>
  )
}

export function KPIs({ w }: { w: LogWidget }) {
  const row = w.rows?.[0] ?? {}
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(170px, 1fr))', gap: 12 }}>
      {w.columns.map((c) => (
        <Card key={c.key} size="small" styles={{ body: { padding: 16 } }}>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>{c.title}</Typography.Text>
          <div style={{ fontSize: 24, fontWeight: 700, marginTop: 4 }}>{cell(c.type, row[c.key] ?? 0)}</div>
        </Card>
      ))}
    </div>
  )
}

// WidgetView shows one widget; onDrill opens the log viewer for a row.
export function WidgetView({ w, devices, onDrill }: { w: LogWidget; devices?: Map<number, string>; onDrill?: (w: LogWidget, row: Record<string, string | number>) => void }) {
  if (w.kind === 'kpis') return <KPIs w={w} />
  const drill = w.drill && onDrill ? (row: Record<string, string | number>) => onDrill(w, row) : undefined
  let body: ReactNode
  if (w.kind === 'series') body = <SeriesChart w={w} />
  else if (w.kind === 'bars') body = <Bars w={w} onDrill={drill} />
  else
    body = (
      <Table
        size="small"
        rowKey={(_, i) => String(i)}
        pagination={false}
        dataSource={w.rows ?? []}
        locale={{ emptyText: 'داده‌ای در این بازه نیست' }}
        scroll={{ x: true }}
        onRow={(r) => (drill ? { onClick: () => drill(r), style: { cursor: 'pointer' } } : {})}
        columns={w.columns.map((c) => ({ title: c.title, dataIndex: c.key, render: (v: unknown) => cell(c.type, v, devices) }))}
      />
    )
  return <Card title={w.title} size="small" styles={{ body: { padding: w.kind === 'table' ? 0 : 16 } }}>{body}</Card>
}
