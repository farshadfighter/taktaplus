import { Card, Table, Tag } from 'antd'
import type { AuditEntry } from '../api'
import { faDateTime } from '../format'
import { Mono, PageHeader } from '../ui'
import { useFetch } from '../useFetch'

const sources: Record<string, string> = { panel: 'پنل', api: 'API', system: 'سیستم' }

export default function Audit() {
  const list = useFetch<{ entries: AuditEntry[] }>('/audit')
  return (
    <>
      <PageHeader title="لاگ ممیزی" subtitle="۲۰۰ رویداد اخیر" />
      <Card styles={{ body: { padding: 0 } }}>
        <Table
          rowKey="id" loading={list.loading} dataSource={list.data?.entries ?? []} scroll={{ x: true }}
          columns={[
            { title: 'زمان', dataIndex: 'at', render: faDateTime },
            { title: 'منبع', dataIndex: 'source', render: (v) => sources[v] ?? v },
            { title: 'کاربر', dataIndex: 'actor', render: (v) => <Mono>{v}</Mono> },
            { title: 'IP', dataIndex: 'ip', render: (v) => <Mono>{v}</Mono> },
            { title: 'عمل', dataIndex: 'action', render: (v: string) => <Tag color={v.includes('fail') || v.includes('revoke') ? 'red' : 'default'}>{v}</Tag> },
            { title: 'شیء', key: 'obj', render: (_, r) => <Mono>{r.object_id}</Mono> },
            { title: 'جزئیات', dataIndex: 'details', render: (d) => <Mono>{Object.keys(d).length ? JSON.stringify(d) : ''}</Mono> },
          ]}
        />
      </Card>
    </>
  )
}
