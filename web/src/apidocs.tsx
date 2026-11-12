import { useMemo, useState } from 'react'
import { Alert, Button, Card, Collapse, Empty, Input, Space, Spin, Table, Tag, Typography } from 'antd'
import { Mono, PageHeader } from './ui'
import { useFetch } from './useFetch'

type Op = { summary: string; description?: string; tags: string[]; 'x-permission'?: string; 'x-access': string }
type Doc = {
  info: { title: string; version: string; description: string }
  servers: { url: string }[]
  tags: { name: string; description?: string }[]
  paths: Record<string, Record<string, Op>>
}
type Row = { key: string; method: string; path: string; op: Op }

const methodColor: Record<string, string> = { get: 'blue', post: 'green', put: 'orange', patch: 'gold', delete: 'red' }
const accessTag: Record<string, [string, string]> = {
  public: ['بدون ورود', 'default'], panel_only: ['فقط پنل', 'red'], install: ['نصب‌ها', 'purple'],
}

// APIDocs shows the product's OpenAPI document without any external asset,
// so it works on an offline network.
export function APIDocs({ product }: { product: string }) {
  const doc = useFetch<Doc>('/openapi.json')
  const [q, setQ] = useState('')
  const base = doc.data?.servers[0]?.url ?? '/api/v1'
  const groups = useMemo(() => {
    const d = doc.data
    if (!d) return []
    const rows: Row[] = []
    for (const [path, ops] of Object.entries(d.paths)) {
      for (const [method, op] of Object.entries(ops)) rows.push({ key: method + path, method, path, op })
    }
    const ql = q.trim().toLowerCase()
    return d.tags.map((t) => ({
      tag: t,
      rows: rows.filter((r) => r.op.tags[0] === t.name && (!ql || r.path.toLowerCase().includes(ql) || r.op.summary.toLowerCase().includes(ql) || (t.description ?? '').includes(q.trim())))
        .sort((a, b) => a.path.localeCompare(b.path) || a.method.localeCompare(b.method)),
    })).filter((g) => g.rows.length > 0)
  }, [doc.data, q])
  const total = groups.reduce((n, g) => n + g.rows.length, 0)
  return (
    <>
      <PageHeader title="مستند API" subtitle={doc.data ? `${doc.data.info.title} · نسخه ${doc.data.info.version} · ${total} مسیر` : product}
        extra={<Button href={base + '/openapi.json'} download="openapi.json">دانلود OpenAPI (JSON)</Button>} />
      {doc.error && <Alert type="error" showIcon title={doc.error} style={{ marginBottom: 12 }} />}
      <Card style={{ marginBottom: 16 }}>
        <Typography.Paragraph>
          درخواست‌ها با توکن API (ساخته‌شده در صفحه‌ی کاربران) و هدر <Mono>Authorization: Bearer tpk_…</Mono> احراز هویت می‌شوند؛ پاسخ‌ها JSON است.
          توکن فقط‌خواندنی فقط درخواست GET می‌فرستد. مسیرهای «فقط پنل» با توکن رد می‌شوند.
        </Typography.Paragraph>
        <pre dir="ltr" style={{ background: '#0F1B2D', color: '#D7E0EC', padding: 10, borderRadius: 6, fontSize: 12, margin: 0, whiteSpace: 'pre-wrap' }}>
          {`curl -H "Authorization: Bearer $TOKEN" ${window.location.origin}${base}/version`}
        </pre>
      </Card>
      <Input.Search allowClear placeholder="جستجوی مسیر یا شرح" value={q} onChange={(e) => setQ(e.target.value)} style={{ maxWidth: 360, marginBottom: 12 }} />
      <Spin spinning={doc.loading}>
        {doc.data && groups.length === 0 && <Empty description="مسیری پیدا نشد" />}
        <Collapse items={groups.map((g) => ({
          key: g.tag.name,
          label: <Space><Typography.Text strong>{g.tag.description || g.tag.name}</Typography.Text><Mono>{g.tag.name}</Mono><Tag>{g.rows.length}</Tag></Space>,
          children: (
            <Table<Row> size="small" rowKey="key" pagination={false} dataSource={g.rows}
              columns={[
                { title: 'متد', width: 80, render: (_, r) => <Tag color={methodColor[r.method]} style={{ minWidth: 56, textAlign: 'center' }}>{r.method.toUpperCase()}</Tag> },
                { title: 'مسیر', render: (_, r) => <Mono>{base}{r.path}</Mono> },
                { title: 'شرح', render: (_, r) => <span dir="ltr">{r.op.summary}</span> },
                { title: 'مجوز', render: (_, r) => (r.op['x-permission'] ? <Mono>{r.op['x-permission']}</Mono> : '—') },
                { title: 'دسترسی', render: (_, r) => { const a = accessTag[r.op['x-access']]; return a ? <Tag color={a[1]}>{a[0]}</Tag> : <Tag color="green">توکن یا پنل</Tag> } },
              ]} />
          ),
        }))} />
      </Spin>
    </>
  )
}
