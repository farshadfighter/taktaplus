import { useState } from 'react'
import { Button, Card, Dropdown, Radio, Table, Typography } from 'antd'
import { Link, useNavigate } from 'react-router'
import type { Change } from '../api'
import { useCan } from '../auth'
import { changeStatusTag, kindLabel } from '../changeui'
import { faDateTime, faNum } from '../format'
import { PageHeader } from '../ui'
import { useFetch } from '../useFetch'

export default function Changes() {
  const navigate = useNavigate()
  const canCreate = useCan('changes.create')
  const [status, setStatus] = useState('')
  const { data, loading } = useFetch<{ items: Change[] }>('/changes?status=' + status)
  const pending = (data?.items ?? []).filter((c) => c.status === 'pending').length

  return (
    <>
      <PageHeader title="درخواست‌های تغییر" subtitle="استقرار سیگنیچر، اسکریپت و ارتقای فرم‌ور با گردش کار تأیید"
        extra={canCreate && (
          <Dropdown menu={{ items: [
            { key: 'd', label: 'استقرار سیگنیچر', onClick: () => navigate('/deploy') },
            { key: 'f', label: 'ارتقای فرم‌ور', onClick: () => navigate('/firmware') },
            { key: 's', label: 'اسکریپت CLI', onClick: () => navigate('/scripts') },
          ] }}>
            <Button type="primary">+ درخواست جدید</Button>
          </Dropdown>
        )} />
      <Card>
        <Radio.Group optionType="button" value={status} onChange={(e) => setStatus(e.target.value)} style={{ marginBottom: 16 }}
          options={[
            { value: '', label: 'همه' }, { value: 'pending', label: `در انتظار تأیید${pending && !status ? ` (${faNum(pending)})` : ''}` },
            { value: 'approved', label: 'زمان‌بندی‌شده' }, { value: 'running', label: 'در حال اجرا' }, { value: 'draft', label: 'پیش‌نویس' },
          ]} />
        <Table<Change> rowKey="id" loading={loading} dataSource={data?.items ?? []} scroll={{ x: 900 }} pagination={{ pageSize: 25, hideOnSinglePage: true }}
          onRow={(c) => ({ onClick: () => navigate(`/changes/${c.id}`), style: { cursor: 'pointer' } })}
          locale={{ emptyText: 'درخواستی نیست' }}
          columns={[
            { title: '#', render: (_, c) => <Link to={`/changes/${c.id}`}>{faNum(c.id)}</Link>, width: 60 },
            { title: 'عنوان', render: (_, c) => <div><div style={{ fontWeight: 600 }}>{c.title}</div><Typography.Text type="secondary" style={{ fontSize: 12 }}>{kindLabel[c.kind]}</Typography.Text></div> },
            { title: 'دستگاه', render: (_, c) => faNum(c.targets) },
            { title: 'وضعیت', render: (_, c) => changeStatusTag(c.status) },
            { title: 'تأیید', render: (_, c) => (c.required_approvals ? `${faNum(c.approvals)} از ${faNum(c.required_approvals)}` : 'لازم نیست') },
            { title: 'نتیجه', render: (_, c) => (c.succeeded_targets + c.failed_targets ? `${faNum(c.succeeded_targets)} موفق · ${faNum(c.failed_targets)} ناموفق` : '—') },
            { title: 'درخواست‌دهنده', dataIndex: 'creator' },
            { title: 'زمان', render: (_, c) => faDateTime(c.created_at) },
          ]} />
      </Card>
    </>
  )
}
