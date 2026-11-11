import { Form, Input, InputNumber, Select, Tag } from 'antd'
import type { FwPolicy, PolicyObjectType, PolicyStatus, PolicyValue } from './api'

export const objectTypeLabel: Record<PolicyObjectType, string> = {
  address: 'Address', addrgrp: 'Address Group', service: 'Service', servicegrp: 'Service Group',
  schedule: 'Schedule', ippool: 'IP Pool', vip: 'VIP',
}

const days = ['sunday', 'monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday']
const dayFa: Record<string, string> = { sunday: 'یکشنبه', monday: 'دوشنبه', tuesday: 'سه‌شنبه', wednesday: 'چهارشنبه', thursday: 'پنجشنبه', friday: 'جمعه', saturday: 'شنبه' }

// valueText is a one-line summary of an object value.
export function valueText(type: PolicyObjectType, v: PolicyValue | undefined): string {
  if (!v) return '—'
  switch (type) {
    case 'address':
      return v.subnet || v.fqdn || (v.start_ip ? `${v.start_ip}-${v.end_ip}` : '') || (v.country ? `کشور ${v.country}` : '—')
    case 'addrgrp':
    case 'servicegrp':
      return (v.members ?? []).join('، ') || '—'
    case 'service': {
      const p = (v.protocol || 'TCP/UDP/SCTP').toUpperCase()
      if (p === 'ICMP') return `ICMP${v.icmp_type !== undefined ? ' ' + v.icmp_type : ''}`
      return [v.tcp_ports && `TCP ${v.tcp_ports}`, v.udp_ports && `UDP ${v.udp_ports}`].filter(Boolean).join(' · ') || p
    }
    case 'schedule':
      return `${(v.days ?? '').split(' ').filter(Boolean).map((d) => dayFa[d] ?? d).join('، ')} ${v.start ?? ''}–${v.end ?? ''}`
    case 'ippool':
      return v.pool_start ? `${v.pool_start}-${v.pool_end}` : '—'
    case 'vip':
      return `${v.ext_ip ?? ''}${v.ext_port ? ':' + v.ext_port : ''} → ${v.mapped_ip ?? ''}${v.mapped_port ? ':' + v.mapped_port : ''}`
  }
}

// ValueFields renders the inputs of one object type under the form path.
export function ValueFields({ type, path, names }: { type: PolicyObjectType; path: (string | number)[]; names?: { addresses: string[]; services: string[] } }) {
  const ltr = { direction: 'ltr' as const }
  const n = (k: string) => [...path, k]
  switch (type) {
    case 'address':
      return (
        <>
          <Form.Item name={n('subnet')} label="Subnet (CIDR)" extra="یکی از Subnet، FQDN، بازه یا کشور را پر کنید."><Input style={ltr} placeholder="10.0.0.0/24" /></Form.Item>
          <Form.Item name={n('fqdn')} label="FQDN"><Input style={ltr} placeholder="example.com" /></Form.Item>
          <Form.Item label="بازه‌ی IP" style={{ marginBottom: 0 }}>
            <Form.Item name={n('start_ip')} style={{ display: 'inline-block', width: 'calc(50% - 4px)' }}><Input style={ltr} placeholder="شروع" /></Form.Item>
            <Form.Item name={n('end_ip')} style={{ display: 'inline-block', width: 'calc(50% - 4px)', marginInlineStart: 8 }}><Input style={ltr} placeholder="پایان" /></Form.Item>
          </Form.Item>
          <Form.Item name={n('country')} label="کشور (کد دوحرفی)"><Input style={{ ...ltr, width: 120 }} maxLength={2} /></Form.Item>
        </>
      )
    case 'addrgrp':
    case 'servicegrp':
      return (
        <Form.Item name={n('members')} label="اعضا" rules={[{ required: true, message: 'اعضای گروه را انتخاب کنید' }]}>
          <Select mode="tags" style={ltr} options={(type === 'addrgrp' ? names?.addresses : names?.services)?.map((x) => ({ value: x, label: x }))} />
        </Form.Item>
      )
    case 'service':
      return (
        <>
          <Form.Item name={n('protocol')} label="پروتکل">
            <Select allowClear placeholder="TCP/UDP/SCTP" options={[{ value: 'TCP/UDP/SCTP', label: 'TCP/UDP' }, { value: 'ICMP', label: 'ICMP' }, { value: 'IP', label: 'IP' }]} />
          </Form.Item>
          <Form.Item name={n('tcp_ports')} label="پورت‌های TCP" extra="مثلاً 443 8000-8080"><Input style={ltr} /></Form.Item>
          <Form.Item name={n('udp_ports')} label="پورت‌های UDP"><Input style={ltr} /></Form.Item>
          <Form.Item name={n('icmp_type')} label="نوع ICMP"><InputNumber min={0} max={255} /></Form.Item>
        </>
      )
    case 'schedule':
      return (
        <>
          <Form.Item name={n('days')} label="روزها" getValueProps={(v?: string) => ({ value: v ? v.split(' ').filter(Boolean) : [] })}
            getValueFromEvent={(v: string[]) => v.join(' ')}>
            <Select mode="multiple" options={days.map((d) => ({ value: d, label: dayFa[d] }))} />
          </Form.Item>
          <Form.Item label="ساعت" style={{ marginBottom: 0 }}>
            <Form.Item name={n('start')} style={{ display: 'inline-block', width: 120 }}><Input style={ltr} placeholder="08:00" /></Form.Item>
            <Form.Item name={n('end')} style={{ display: 'inline-block', width: 120, marginInlineStart: 8 }}><Input style={ltr} placeholder="17:00" /></Form.Item>
          </Form.Item>
        </>
      )
    case 'ippool':
      return (
        <Form.Item label="بازه‌ی Pool" style={{ marginBottom: 0 }}>
          <Form.Item name={n('pool_start')} style={{ display: 'inline-block', width: 'calc(50% - 4px)' }}><Input style={ltr} placeholder="شروع" /></Form.Item>
          <Form.Item name={n('pool_end')} style={{ display: 'inline-block', width: 'calc(50% - 4px)', marginInlineStart: 8 }}><Input style={ltr} placeholder="پایان" /></Form.Item>
        </Form.Item>
      )
    case 'vip':
      return (
        <>
          <Form.Item name={n('ext_zone')} label="اینترفیس منطقی بیرونی" extra="مثلاً wan؛ با نگاشت اینترفیس به پورت واقعی تبدیل می‌شود."><Input style={ltr} /></Form.Item>
          <Form.Item label="آدرس بیرونی / پورت" style={{ marginBottom: 0 }}>
            <Form.Item name={n('ext_ip')} style={{ display: 'inline-block', width: 'calc(65% - 4px)' }}><Input style={ltr} /></Form.Item>
            <Form.Item name={n('ext_port')} style={{ display: 'inline-block', width: 'calc(35% - 4px)', marginInlineStart: 8 }}><Input style={ltr} placeholder="پورت" /></Form.Item>
          </Form.Item>
          <Form.Item label="آدرس داخلی / پورت" style={{ marginBottom: 0 }}>
            <Form.Item name={n('mapped_ip')} style={{ display: 'inline-block', width: 'calc(65% - 4px)' }}><Input style={ltr} /></Form.Item>
            <Form.Item name={n('mapped_port')} style={{ display: 'inline-block', width: 'calc(35% - 4px)', marginInlineStart: 8 }}><Input style={ltr} placeholder="پورت" /></Form.Item>
          </Form.Item>
        </>
      )
  }
}

// cleanValue drops empty fields so the server sees only what was set.
export function cleanValue(v: PolicyValue | undefined): PolicyValue {
  const out: Record<string, unknown> = {}
  for (const [k, x] of Object.entries(v ?? {})) {
    if (x === undefined || x === null || x === '' || (Array.isArray(x) && x.length === 0)) continue
    out[k] = typeof x === 'string' ? x.trim() : x
  }
  return out as PolicyValue
}

export const policyStateTag = (s: PolicyStatus['state']) => {
  const m: Record<string, [string, string]> = {
    in_sync: ['هم‌گام', 'green'], local_changes: ['تغییر محلی', 'red'], outdated: ['نسخه‌ی قدیمی', 'orange'], not_installed: ['نصب نشده', 'default'],
  }
  const [t, c] = m[s] ?? [s, 'default']
  return <Tag color={c}>{t}</Tag>
}

export const opLabel: Record<string, [string, string]> = {
  create: ['ایجاد', 'green'], update: ['ویرایش', 'blue'], delete: ['حذف', 'red'], move: ['جابه‌جایی', 'purple'],
}

export const profileText = (p: FwPolicy) =>
  [p.av_profile && 'AV', p.webfilter_profile && 'WebFilter', p.ips_sensor && 'IPS', p.application_list && 'App', p.ssl_ssh_profile && 'SSL']
    .filter(Boolean).join('، ')

export function ObjChips({ names }: { names: string[] | undefined }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: 2 }}>
      {(names ?? []).map((n) => (
        <span key={n} dir="ltr" style={{ whiteSpace: 'nowrap', fontFamily: 'ui-monospace, Consolas, monospace', fontSize: 12, background: '#F1F4F9', border: '1px solid #E3E8EF', borderRadius: 4, padding: '0 6px' }}>{n}</span>
      ))}
    </div>
  )
}

// deviceNumber is the last number in a device name, used by mapping patterns.
export const deviceNumber = (name: string) => name.match(/(\d+)(?!.*\d)/)?.[1] ?? ''
