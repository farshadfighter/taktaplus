import { useState } from 'react'
import { Alert, Button, Card, Form, Input, InputNumber, Modal, Popconfirm, Select, Space, Switch, Table, Tabs, Tag, Typography, App as AntApp } from 'antd'
import { Link, useNavigate } from 'react-router'
import { api, type Backup, type DeviceRow, type Group, type Policy, type RestoreJob } from '../api'
import { useCan } from '../auth'
import { faDateTime, faNum } from '../format'
import { Mono, PageHeader, bytesText } from '../ui'
import { useFetch } from '../useFetch'

export const reasonLabel: Record<string, string> = {
  scheduled: 'زمان‌بندی‌شده', manual: 'دستی', pre_restore: 'قبل از ریستور', on_change: 'تغییر کانفیگ',
}

export function BackupTable({ items, loading, hideDevice }: { items: Backup[]; loading?: boolean; hideDevice?: boolean }) {
  const navigate = useNavigate()
  return (
    <Table<Backup>
      rowKey="id"
      size="small"
      loading={loading}
      dataSource={items}
      scroll={{ x: 800 }}
      pagination={{ pageSize: 25, hideOnSinglePage: true }}
      locale={{ emptyText: 'بکاپی ثبت نشده' }}
      onRow={(b) => (b.status === 'ok' ? { onClick: () => navigate(`/backups/${b.id}`), style: { cursor: 'pointer' } } : {})}
      columns={[
        { title: 'زمان', render: (_, b) => (b.status === 'ok' ? <Link to={`/backups/${b.id}`}>{faDateTime(b.at)}</Link> : faDateTime(b.at)) },
        ...(hideDevice ? [] : [{ title: 'دستگاه', render: (_: unknown, b: Backup) => <Link to={`/devices/${b.device_id}`} onClick={(e) => e.stopPropagation()}>{b.device_name}</Link> }]),
        { title: 'علت', render: (_, b) => reasonLabel[b.reason] ?? b.reason },
        { title: 'توسط', dataIndex: 'actor' },
        {
          title: 'وضعیت', render: (_, b) =>
            b.status === 'ok' ? (
              <Space size={4}><Tag color="green">موفق</Tag>{b.locked && <Tag color="purple">قفل</Tag>}</Space>
            ) : (
              <Typography.Text type="danger" style={{ fontSize: 12 }}>ناموفق: {b.error}</Typography.Text>
            ),
        },
        { title: 'حجم', render: (_, b) => (b.status === 'ok' ? <Mono>{bytesText(b.size)}</Mono> : '—') },
        { title: 'فرم‌ور', render: (_, b) => (b.firmware ? <Mono>v{b.firmware}</Mono> : '—') },
        { title: 'یادداشت', dataIndex: 'note', ellipsis: true },
      ]}
    />
  )
}

const schedulePresets = [
  { value: '30 1 * * *', label: 'هر روز ساعت ۱:۳۰' },
  { value: '0 */6 * * *', label: 'هر ۶ ساعت' },
  { value: '0 * * * *', label: 'هر ساعت' },
  { value: '0 2 * * 5', label: 'هفتگی، جمعه ساعت ۲' },
]

function PolicyModal({ policy, onClose, groups, devices }: { policy: Partial<Policy> | null; onClose: (saved: boolean) => void; groups: Group[]; devices: DeviceRow[] }) {
  const [form] = Form.useForm()
  const [error, setError] = useState<string | null>(null)
  const scope = Form.useWatch('scope', form)
  const save = async (v: { name: string; scope: string; group_id?: number; device_id?: number; schedule: string; keep_count: number; keep_days: number; enabled: boolean }) => {
    const body = {
      name: v.name, schedule: v.schedule.trim(), keep_count: v.keep_count ?? 0, keep_days: v.keep_days ?? 0, enabled: v.enabled,
      group_id: v.scope === 'group' ? v.group_id ?? null : null, device_id: v.scope === 'device' ? v.device_id ?? null : null,
    }
    try {
      if (policy?.id) await api.put(`/backup-policies/${policy.id}`, body)
      else await api.post('/backup-policies', body)
      onClose(true)
    } catch (e) {
      setError((e as Error).message)
    }
  }
  return (
    <Modal open={policy !== null} title={policy?.id ? 'ویرایش سیاست بکاپ' : 'سیاست بکاپ جدید'} okText="ذخیره" cancelText="انصراف"
      onCancel={() => onClose(false)} onOk={() => form.submit()} destroyOnHidden afterOpenChange={(o) => {
        if (!o) return
        setError(null)
        form.setFieldsValue({
          name: policy?.name ?? '', schedule: policy?.schedule ?? '30 1 * * *', keep_count: policy?.keep_count ?? 30, keep_days: policy?.keep_days ?? 0,
          enabled: policy?.enabled ?? true, group_id: policy?.group_id ?? undefined, device_id: policy?.device_id ?? undefined,
          scope: policy?.device_id ? 'device' : policy?.group_id ? 'group' : 'all',
        })
      }}>
      {error && <Alert type="error" title={error} showIcon style={{ marginBottom: 12 }} />}
      <Form form={form} layout="vertical" requiredMark={false} onFinish={save}>
        <Form.Item name="name" label="نام" rules={[{ required: true, message: 'نام سیاست' }]}><Input /></Form.Item>
        <Form.Item name="scope" label="دامنه">
          <Select options={[{ value: 'all', label: 'همه‌ی دستگاه‌ها' }, { value: 'group', label: 'یک گروه' }, { value: 'device', label: 'یک دستگاه' }]} />
        </Form.Item>
        {scope === 'group' && <Form.Item name="group_id" label="گروه" rules={[{ required: true }]}><Select options={groups.map((g) => ({ value: g.id, label: g.name }))} /></Form.Item>}
        {scope === 'device' && <Form.Item name="device_id" label="دستگاه" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={devices.map((d) => ({ value: d.id, label: d.name }))} /></Form.Item>}
        <Form.Item name="schedule" label="زمان‌بندی (Cron: دقیقه ساعت روز ماه روزِهفته)" rules={[{ required: true }]}
          extra={<Space wrap size={4} style={{ marginTop: 6 }}>{schedulePresets.map((p) => <Tag key={p.value} style={{ cursor: 'pointer' }} onClick={() => form.setFieldValue('schedule', p.value)}>{p.label}</Tag>)}</Space>}>
          <Input dir="ltr" />
        </Form.Item>
        <Space size={16} wrap>
          <Form.Item name="keep_count" label="نگهداری تعداد (۰ = نامحدود)"><InputNumber min={0} max={10000} /></Form.Item>
          <Form.Item name="keep_days" label="نگهداری روز (۰ = نامحدود)"><InputNumber min={0} max={3650} /></Form.Item>
          <Form.Item name="enabled" label="فعال" valuePropName="checked"><Switch /></Form.Item>
        </Space>
        <Typography.Paragraph type="secondary" style={{ fontSize: 12, margin: 0 }}>
          برای هر دستگاه دقیق‌ترین سیاست (دستگاه، سپس گروه، سپس همه) قاعده‌ی نگهداری را تعیین می‌کند. بکاپ‌های قفل‌شده و آخرین بکاپ هرگز حذف نمی‌شوند.
        </Typography.Paragraph>
      </Form>
    </Modal>
  )
}

const jobTag = (s: string) =>
  ({ running: <Tag color="blue">در حال اجرا</Tag>, waiting: <Tag color="gold">انتظار برای ریبوت</Tag>, succeeded: <Tag color="green">موفق</Tag>, failed: <Tag color="red">ناموفق</Tag> })[s] ?? <Tag>{s}</Tag>

export default function Backups() {
  const { message } = AntApp.useApp()
  const canAdmin = useCan('backups.admin')
  const [device, setDevice] = useState<number | null>(null)
  const backups = useFetch<{ items: Backup[] }>('/backups' + (device ? `?device=${device}` : ''))
  const policies = useFetch<{ items: Policy[] }>('/backup-policies')
  const restores = useFetch<{ items: RestoreJob[] }>('/restores')
  const devices = useFetch<{ items: DeviceRow[] }>('/devices')
  const groups = useFetch<{ items: Group[] }>('/groups')
  const [editing, setEditing] = useState<Partial<Policy> | null>(null)
  const scopeText = (p: Policy) =>
    p.device_id ? `دستگاه: ${devices.data?.items.find((d) => d.id === p.device_id)?.name ?? p.device_id}`
      : p.group_id ? `گروه: ${groups.data?.items.find((g) => g.id === p.group_id)?.name ?? p.group_id}` : 'همه‌ی دستگاه‌ها'
  const items = backups.data?.items ?? []
  const failed = items.filter((b) => b.status === 'failed').length

  return (
    <>
      <PageHeader title="بکاپ و ریستور" subtitle={`${faNum(items.length - failed)} بکاپ${failed ? ` · ${faNum(failed)} ناموفق` : ''}`} />
      <Tabs items={[
        {
          key: 'list', label: 'بکاپ‌ها', children: (
            <Card>
              <Select allowClear showSearch optionFilterProp="label" placeholder="همه‌ی دستگاه‌ها" style={{ width: 240, marginBottom: 16 }} value={device} onChange={setDevice}
                options={(devices.data?.items ?? []).map((d) => ({ value: d.id, label: d.name }))} />
              <BackupTable items={items} loading={backups.loading} />
            </Card>
          ),
        },
        {
          key: 'policies', label: 'سیاست‌های زمان‌بندی', children: (
            <Card extra={canAdmin && <Button type="primary" onClick={() => setEditing({})}>+ سیاست جدید</Button>}>
              <Table<Policy> rowKey="id" size="small" pagination={false} dataSource={policies.data?.items ?? []} scroll={{ x: 800 }}
                columns={[
                  { title: 'نام', dataIndex: 'name' },
                  { title: 'دامنه', render: (_, p) => scopeText(p) },
                  { title: 'زمان‌بندی', render: (_, p) => <Mono>{p.schedule}</Mono> },
                  { title: 'نگهداری', render: (_, p) => [p.keep_count ? `${faNum(p.keep_count)} نسخه` : '', p.keep_days ? `${faNum(p.keep_days)} روز` : ''].filter(Boolean).join(' · ') || 'نامحدود' },
                  { title: 'اجرای بعدی', render: (_, p) => (p.enabled ? faDateTime(p.next_run_at) : <Tag>غیرفعال</Tag>) },
                  { title: 'آخرین اجرا', render: (_, p) => faDateTime(p.last_run_at) },
                  ...(canAdmin ? [{
                    title: '', render: (_: unknown, p: Policy) => (
                      <Space>
                        <Button size="small" type="link" onClick={() => setEditing(p)}>ویرایش</Button>
                        <Popconfirm title="سیاست حذف شود؟ بکاپ‌های موجود باقی می‌مانند." okText="حذف" cancelText="انصراف"
                          onConfirm={async () => { await api.del(`/backup-policies/${p.id}`).catch((e) => message.error(e.message)); void policies.reload() }}>
                          <Button size="small" type="link" danger>حذف</Button>
                        </Popconfirm>
                      </Space>
                    ),
                  }] : []),
                ]} />
            </Card>
          ),
        },
        {
          key: 'restores', label: 'تاریخچه‌ی ریستور', children: (
            <Card>
              <Table<RestoreJob> rowKey="id" size="small" dataSource={restores.data?.items ?? []} pagination={{ pageSize: 20, hideOnSinglePage: true }}
                locale={{ emptyText: 'ریستوری انجام نشده' }} scroll={{ x: 800 }}
                columns={[
                  { title: 'زمان', render: (_, j) => faDateTime(j.started_at) },
                  { title: 'دستگاه', render: (_, j) => <Link to={`/devices/${j.device_id}`}>{j.device_name}</Link> },
                  { title: 'بکاپ', render: (_, j) => <Link to={`/backups/${j.backup_id}`}>#{faNum(j.backup_id)}</Link> },
                  { title: 'بکاپ ایمنی', render: (_, j) => (j.pre_backup_id ? <Link to={`/backups/${j.pre_backup_id}`}>#{faNum(j.pre_backup_id)}</Link> : '—') },
                  { title: 'توسط', dataIndex: 'actor' },
                  { title: 'وضعیت', render: (_, j) => <Space orientation="vertical" size={0}>{jobTag(j.status)}{j.error && <Typography.Text type="danger" style={{ fontSize: 12 }}>{j.error}</Typography.Text>}</Space> },
                ]} />
            </Card>
          ),
        },
      ]} />
      <PolicyModal policy={editing} groups={groups.data?.items ?? []} devices={devices.data?.items ?? []}
        onClose={(saved) => { setEditing(null); if (saved) void policies.reload() }} />
    </>
  )
}
