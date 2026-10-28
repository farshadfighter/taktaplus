import { useEffect, useMemo, useState } from 'react'
import { Alert, AutoComplete, Button, Descriptions, Drawer, Input, Popover, Segmented, Select, Space, Table, Tag, Typography } from 'antd'
import { useSearchParams } from 'react-router'
import { api, type LogCatalog, type LogFilter, type LogRecord, type LogSearchResult } from '../api'
import { faDateTime, faNum } from '../format'
import { DeviceSelect, PeriodPicker, cell, periodOf, rangeQuery, useDevices, type Period } from '../logui'
import { Mono, PageHeader } from '../ui'
import { useFetch } from '../useFetch'

const opLabel: Record<LogFilter['op'], string> = { eq: '=', ne: '≠', contains: 'شامل' }
const pageSize = 100

function FilterAdder({ fields, onAdd }: { fields: string[]; onAdd: (f: LogFilter) => void }) {
  const [open, setOpen] = useState(false)
  const [f, setF] = useState<LogFilter>({ field: '', op: 'eq', value: '' })
  const add = () => {
    if (!f.field.trim()) return
    onAdd({ ...f, field: f.field.trim() })
    setF({ field: '', op: 'eq', value: '' })
    setOpen(false)
  }
  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      trigger="click"
      title="افزودن شرط"
      content={
        <Space orientation="vertical" style={{ width: 280 }}>
          <AutoComplete
            placeholder="فیلد (مثلاً srcip یا هر کلید لاگ)"
            value={f.field}
            onChange={(v) => setF({ ...f, field: v })}
            options={fields.filter((x) => x.includes(f.field)).map((x) => ({ value: x }))}
            style={{ width: '100%' }}
          />
          <Select value={f.op} onChange={(op) => setF({ ...f, op })} options={(['eq', 'ne', 'contains'] as const).map((o) => ({ value: o, label: opLabel[o] }))} style={{ width: '100%' }} />
          <Input placeholder="مقدار" value={f.value} onChange={(e) => setF({ ...f, value: e.target.value })} onPressEnter={add} dir="auto" />
          <Button type="primary" block onClick={add}>افزودن</Button>
        </Space>
      }
    >
      <Button size="small">+ شرط</Button>
    </Popover>
  )
}

export default function LogView() {
  const [params, setParams] = useSearchParams()
  const catalog = useFetch<LogCatalog>('/logs/catalog')
  const { list, names } = useDevices()
  const type = params.get('type') ?? 'traffic'
  const [period, setPeriod] = useState<Period>(() =>
    params.get('from') && params.get('to') ? periodOf('custom', params.get('from')!, params.get('to')!) : periodOf('1h'))
  const [devs, setDevs] = useState<number[]>(() => (params.get('devices') ?? '').split(',').filter(Boolean).map(Number))
  const [filters, setFilters] = useState<LogFilter[]>(() => {
    try {
      return JSON.parse(params.get('f') ?? '[]')
    } catch {
      return []
    }
  })
  const [page, setPage] = useState(1)
  const [res, setRes] = useState<LogSearchResult | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [selected, setSelected] = useState<LogRecord | null>(null)
  const [nonce, setNonce] = useState(0)

  const view = catalog.data?.views.find((v) => v.id === type)
  const query = useMemo(() => {
    const q = new URLSearchParams(rangeQuery(period, devs))
    q.set('type', type)
    if (filters.length) q.set('filters', JSON.stringify(filters))
    return q
  }, [period, devs, type, filters])

  useEffect(() => {
    let live = true
    setLoading(true)
    const q = new URLSearchParams(query)
    q.set('limit', String(pageSize))
    q.set('offset', String((page - 1) * pageSize))
    api
      .get<LogSearchResult>('/logs/search?' + q.toString())
      .then((r) => { if (live) { setRes(r); setError(null) } })
      .catch((e) => { if (live) setError(e.message) })
      .finally(() => { if (live) setLoading(false) })
    return () => { live = false }
  }, [query, page, nonce])

  const setType = (t: string) => {
    setFilters([])
    setPage(1)
    setParams({ type: t })
  }
  const addFilter = (f: LogFilter) => {
    setFilters((fs) => [...fs.filter((x) => !(x.field === f.field && x.op === f.op)), f])
    setPage(1)
  }

  const fields = useMemo(() => {
    const set = new Set<string>(view?.columns.map((c) => c.key) ?? [])
    res?.records.forEach((r) => Object.keys(r.fields ?? {}).forEach((k) => set.add(k)))
    return [...set].sort()
  }, [view, res])

  return (
    <>
      <PageHeader
        title="نمایشگر لاگ"
        extra={
          <>
            <Segmented value={type} onChange={(v) => setType(String(v))} options={(catalog.data?.views ?? []).map((v) => ({ value: v.id, label: v.title }))} />
          </>
        }
      />
      <Space wrap style={{ marginBottom: 12 }}>
        <PeriodPicker value={period} onChange={(p) => { setPeriod(p); setPage(1) }} />
        <DeviceSelect value={devs} onChange={(d) => { setDevs(d); setPage(1) }} devices={list} />
        <Button type="primary" onClick={() => { setPeriod(period.preset === 'custom' ? period : periodOf(period.preset)); setNonce((n) => n + 1) }}>جستجو</Button>
        <Button href={'/api/v1/logs/export.csv?' + query.toString()}>خروجی CSV</Button>
      </Space>
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 8, marginBottom: 12 }}>
        {filters.map((f, i) => (
          <Tag key={i} closable color="magenta" onClose={() => { setFilters(filters.filter((_, j) => j !== i)); setPage(1) }} style={{ fontSize: 13, padding: '2px 8px' }}>
            <Mono>{f.field}</Mono> {opLabel[f.op]} <span dir="auto">{f.value}</span>
          </Tag>
        ))}
        <FilterAdder fields={fields} onAdd={addFilter} />
        {res && (
          <Typography.Text type="secondary" style={{ marginInlineStart: 'auto' }}>
            {res.capped ? 'بیش از ' : ''}{faNum(res.total)} رکورد · {faNum(Math.round(res.took_ms) / 1000)} ثانیه
          </Typography.Text>
        )}
      </div>
      {error && <Alert type="error" showIcon title={error} style={{ marginBottom: 12 }} />}
      <Table<LogRecord>
        size="small"
        loading={loading}
        rowKey={(r, i) => `${r.ts}-${i}`}
        dataSource={res?.records ?? []}
        scroll={{ x: true }}
        onRow={(r) => ({ onClick: () => setSelected(r), style: { cursor: 'pointer', background: selected === r ? '#FBEFF3' : undefined } })}
        pagination={{ current: page, pageSize, total: Math.min(res?.total ?? 0, 10_000 + pageSize), showSizeChanger: false, onChange: setPage }}
        columns={[
          { title: 'زمان', dataIndex: 'ts', render: (v: string) => <span style={{ whiteSpace: 'nowrap' }}>{faDateTime(v)}</span> },
          { title: 'دستگاه', dataIndex: 'device_id', render: (v: number) => names.get(v) ?? v },
          ...(view?.columns ?? []).map((c) => ({ title: c.title, dataIndex: c.key, render: (v: unknown) => cell(c.type, v) })),
        ]}
      />
      <Drawer open={selected !== null} onClose={() => setSelected(null)} title="جزئیات رکورد" size={480}>
        {selected && (
          <>
            <Descriptions size="small" column={1} bordered items={[
              { key: 'ts', label: 'زمان', children: faDateTime(selected.ts) },
              { key: 'dev', label: 'دستگاه', children: names.get(selected.device_id) ?? selected.device_id },
            ]} />
            <Table
              size="small"
              style={{ marginTop: 12 }}
              rowKey="k"
              pagination={false}
              dataSource={Object.entries(selected.fields ?? {}).sort(([a], [b]) => a.localeCompare(b)).map(([k, v]) => ({ k, v }))}
              columns={[
                { title: 'فیلد', dataIndex: 'k', render: (k: string) => <Mono>{k}</Mono> },
                { title: 'مقدار', dataIndex: 'v', render: (v: string) => <span dir="auto" style={{ wordBreak: 'break-all' }}>{v}</span> },
                {
                  title: '', dataIndex: 'k', width: 70,
                  render: (k: string, r: { v: string }) => <Button size="small" type="link" onClick={() => { addFilter({ field: k, op: 'eq', value: r.v }); setSelected(null) }}>فیلتر</Button>,
                },
              ]}
            />
          </>
        )}
      </Drawer>
    </>
  )
}
