import { useMemo, useState } from 'react'
import { Alert, Button, Card, Input, Modal, Form, Select, Space, Table, Tag, Typography, App as AntApp, Popconfirm } from 'antd'
import { Link, useNavigate } from 'react-router'
import { api, type DeviceRow, type Group } from '../api'
import { useCan } from '../auth'
import { faDateTime, faNum } from '../format'
import { Mono, PageHeader, Usage, kindLabel, licenseTag, statusTag } from '../ui'
import { useFetch } from '../useFetch'

function Groups({ open, onClose, groups, reload }: { open: boolean; onClose: () => void; groups: Group[]; reload: () => void }) {
  const { message } = AntApp.useApp()
  const [form] = Form.useForm()
  const [editing, setEditing] = useState<Group | null>(null)
  const save = async (v: { name: string; description?: string }) => {
    try {
      if (editing) await api.put(`/groups/${editing.id}`, { name: v.name, description: v.description ?? '' })
      else await api.post('/groups', { name: v.name, description: v.description ?? '' })
      form.resetFields()
      setEditing(null)
      reload()
    } catch (e) {
      message.error((e as Error).message)
    }
  }
  return (
    <Modal open={open} onCancel={onClose} footer={null} title="گروه‌های تجهیزات" width={560}>
      <Table<Group>
        rowKey="id"
        size="small"
        pagination={false}
        dataSource={groups}
        columns={[
          { title: 'نام', dataIndex: 'name' },
          { title: 'دستگاه', dataIndex: 'devices', render: (n: number) => faNum(n), width: 80 },
          {
            title: '', width: 150,
            render: (_, g) => (
              <Space>
                <Button size="small" type="link" onClick={() => { setEditing(g); form.setFieldsValue(g) }}>ویرایش</Button>
                <Popconfirm title="گروه حذف شود؟ دستگاه‌ها بدون گروه می‌شوند." okText="حذف" cancelText="انصراف"
                  onConfirm={async () => { await api.del(`/groups/${g.id}`).catch((e) => message.error(e.message)); reload() }}>
                  <Button size="small" type="link" danger>حذف</Button>
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />
      <Form form={form} layout="inline" onFinish={save} style={{ marginTop: 16, rowGap: 8 }}>
        <Form.Item name="name" rules={[{ required: true, message: 'نام گروه' }]}><Input placeholder="نام گروه" /></Form.Item>
        <Form.Item name="description"><Input placeholder="توضیح" /></Form.Item>
        <Button type="primary" htmlType="submit">{editing ? 'ذخیره' : 'افزودن'}</Button>
        {editing && <Button onClick={() => { setEditing(null); form.resetFields() }}>انصراف</Button>}
      </Form>
    </Modal>
  )
}

export default function Devices() {
  const navigate = useNavigate()
  const canWrite = useCan('devices.write')
  const [q, setQ] = useState('')
  const [group, setGroup] = useState<number | null>(null)
  const [kind, setKind] = useState<string | null>(null)
  const [status, setStatus] = useState<string | null>(null)
  const [groupsOpen, setGroupsOpen] = useState(false)
  const path = '/devices' + (group ? `?group=${group}` : '')
  const { data, error, loading } = useFetch<{ items: DeviceRow[] }>(path)
  const groups = useFetch<{ items: Group[] }>('/groups')

  const rows = useMemo(() => {
    const term = q.trim().toLowerCase()
    return (data?.items ?? []).filter(
      (d) =>
        (!kind || d.kind === kind) &&
        (!status || d.status === status || (status === 'unlicensed' && !['active', 'grace'].includes(d.license))) &&
        (!term || [d.name, d.host, d.serial, d.model, d.hostname].some((v) => v.toLowerCase().includes(term))),
    )
  }, [data, q, kind, status])

  const all = data?.items ?? []
  const fg = all.filter((d) => d.kind === 'fortigate').length

  return (
    <>
      <PageHeader
        title="تجهیزات"
        subtitle={`${faNum(all.length)} دستگاه · ${faNum(fg)} FortiGate · ${faNum(all.length - fg)} FortiWeb`}
        extra={
          canWrite && (
            <>
              <Button onClick={() => setGroupsOpen(true)}>مدیریت گروه‌ها</Button>
              <Button type="primary" onClick={() => navigate('/devices/new')}>+ افزودن دستگاه</Button>
            </>
          )
        }
      />
      {error && <Alert type="error" title={error} showIcon style={{ marginBottom: 16 }} />}
      <Card>
        <Space wrap style={{ marginBottom: 16 }}>
          <Input.Search placeholder="نام، آدرس، سریال یا مدل" allowClear onChange={(e) => setQ(e.target.value)} style={{ width: 260 }} />
          <Select allowClear placeholder="همه‌ی گروه‌ها" style={{ width: 170 }} value={group} onChange={setGroup}
            options={(groups.data?.items ?? []).map((g) => ({ value: g.id, label: g.name }))} />
          <Select allowClear placeholder="همه‌ی انواع" style={{ width: 140 }} value={kind} onChange={setKind}
            options={[{ value: 'fortigate', label: 'FortiGate' }, { value: 'fortiweb', label: 'FortiWeb' }]} />
          <Select allowClear placeholder="همه‌ی وضعیت‌ها" style={{ width: 160 }} value={status} onChange={setStatus}
            options={[
              { value: 'online', label: 'آنلاین' }, { value: 'offline', label: 'آفلاین' },
              { value: 'error', label: 'خطای اتصال' }, { value: 'unlicensed', label: 'بدون لایسنس فعال' },
            ]} />
        </Space>
        <Table<DeviceRow>
          rowKey="id"
          loading={loading}
          dataSource={rows}
          scroll={{ x: 1100 }}
          pagination={{ pageSize: 25, hideOnSinglePage: true }}
          onRow={(d) => ({ onClick: () => navigate(`/devices/${d.id}`), style: { cursor: 'pointer' } })}
          locale={{ emptyText: canWrite ? <span>هنوز دستگاهی ثبت نشده است. <Link to="/devices/new">افزودن دستگاه</Link></span> : 'دستگاهی نیست' }}
          columns={[
            {
              title: 'نام', render: (_, d) => (
                <div>
                  <Link to={`/devices/${d.id}`} onClick={(e) => e.stopPropagation()} style={{ fontWeight: 600 }}>{d.name}</Link>
                  <div><Typography.Text type="secondary" style={{ fontSize: 12 }}>{kindLabel(d.kind)} {d.model && `· ${d.model}`}</Typography.Text></div>
                </div>
              ),
            },
            { title: 'آدرس', render: (_, d) => <Mono>{d.host}</Mono> },
            { title: 'سریال', render: (_, d) => (d.serial ? <Mono>{d.serial}</Mono> : '—') },
            { title: 'فرم‌ور', render: (_, d) => (d.firmware ? <Mono>v{d.firmware}</Mono> : '—') },
            { title: 'وضعیت', render: (_, d) => <Space size={4}>{statusTag(d.status)}{d.ha_mode && d.ha_mode !== 'standalone' && <Tag>HA</Tag>}</Space> },
            { title: 'CPU', render: (_, d) => <Usage value={d.health?.cpu} />, width: 140 },
            { title: 'حافظه', render: (_, d) => <Usage value={d.health?.mem} />, width: 140 },
            { title: 'لایسنس', render: (_, d) => licenseTag(d.license) },
            { title: 'گروه', render: (_, d) => d.group_name || '—' },
            { title: 'آخرین ارتباط', render: (_, d) => faDateTime(d.last_seen_at) },
          ]}
        />
      </Card>
      <Groups open={groupsOpen} onClose={() => setGroupsOpen(false)} groups={groups.data?.items ?? []} reload={groups.reload} />
    </>
  )
}
