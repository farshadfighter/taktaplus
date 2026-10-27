import { useState } from 'react'
import { Button, Card, Form, Input, Modal, Popconfirm, Select, Table, Tabs, Tag, Typography, App as AntApp } from 'antd'
import { Link, useNavigate } from 'react-router'
import { api, type DeviceRow, type Group, type Script, type Variable } from '../api'
import { useCan } from '../auth'
import { faDateTime } from '../format'
import { Mono, PageHeader } from '../ui'
import { useFetch } from '../useFetch'

const scopeLabel: Record<string, string> = { global: 'سراسری', group: 'گروه', device: 'دستگاه' }

export default function Scripts() {
  const { message } = AntApp.useApp()
  const navigate = useNavigate()
  const canCreate = useCan('changes.create')
  const { data, loading, reload } = useFetch<{ items: Script[]; variables: Variable[] }>('/scripts')
  const devices = useFetch<{ items: DeviceRow[] }>('/devices')
  const groups = useFetch<{ items: Group[] }>('/groups')
  const [varOpen, setVarOpen] = useState(false)
  const [form] = Form.useForm()
  const scope = Form.useWatch('scope', form)
  const scopeName = (v: Variable) =>
    v.scope === 'global' ? '' : v.scope === 'group' ? groups.data?.items.find((g) => g.id === v.scope_id)?.name : devices.data?.items.find((d) => d.id === v.scope_id)?.name

  return (
    <>
      <PageHeader title="اسکریپت‌های CLI" subtitle="قالب Go با متغیرها، مثل {{ .Device.Hostname }} یا {{ .Vars.dns }}؛ اسکریپت‌های فقط‌خواندنی بدون گردش کار اجرا می‌شوند."
        extra={canCreate && <Button type="primary" onClick={() => navigate('/scripts/new')}>+ اسکریپت جدید</Button>} />
      <Tabs items={[
        {
          key: 'lib', label: 'کتابخانه', children: (
            <Card>
              <Table<Script> rowKey="id" loading={loading} dataSource={data?.items ?? []} pagination={false}
                onRow={(s) => ({ onClick: () => navigate(`/scripts/${s.id}`), style: { cursor: 'pointer' } })}
                locale={{ emptyText: 'اسکریپتی نیست' }}
                columns={[
                  { title: 'نام', render: (_, s) => <Link to={`/scripts/${s.id}`}>{s.name}</Link> },
                  { title: 'نوع', render: (_, s) => (s.read_only ? <Tag color="green">فقط‌خواندنی</Tag> : <Tag color="orange">تغییر پیکربندی (با تأیید)</Tag>) },
                  { title: 'پلتفرم', render: (_, s) => (s.platform === 'fortiweb' ? 'FortiWeb' : 'FortiGate') },
                  { title: 'توضیح', dataIndex: 'description', ellipsis: true },
                  { title: 'آخرین تغییر', render: (_, s) => `${s.updated_by} · ${faDateTime(s.updated_at)}` },
                ]} />
            </Card>
          ),
        },
        {
          key: 'vars', label: 'متغیرها', children: (
            <Card extra={canCreate && <Button onClick={() => { form.resetFields(); form.setFieldsValue({ scope: 'global' }); setVarOpen(true) }}>+ متغیر</Button>}>
              <Typography.Paragraph type="secondary">مقدار دستگاه بر گروه و مقدار گروه بر سراسری مقدم است.</Typography.Paragraph>
              <Table<Variable> rowKey="id" size="small" pagination={false} dataSource={data?.variables ?? []} locale={{ emptyText: 'متغیری تعریف نشده' }}
                columns={[
                  { title: 'نام', render: (_, v) => <Mono>{v.name}</Mono> },
                  { title: 'سطح', render: (_, v) => `${scopeLabel[v.scope]} ${scopeName(v) ?? ''}` },
                  { title: 'مقدار', render: (_, v) => <Mono>{v.value}</Mono> },
                  ...(canCreate ? [{
                    title: '', render: (_: unknown, v: Variable) => (
                      <Popconfirm title="متغیر حذف شود؟" okText="حذف" cancelText="انصراف" onConfirm={async () => { await api.del(`/variables/${v.id}`).catch((e) => message.error(e.message)); void reload() }}>
                        <Button size="small" type="link" danger>حذف</Button>
                      </Popconfirm>
                    ),
                  }] : []),
                ]} />
            </Card>
          ),
        },
      ]} />
      <Modal open={varOpen} title="تعریف متغیر" okText="ذخیره" cancelText="انصراف" onCancel={() => setVarOpen(false)} onOk={() => form.submit()}>
        <Form form={form} layout="vertical" requiredMark={false} onFinish={async (v) => {
          try {
            await api.post('/variables', { scope: v.scope, scope_id: v.scope_id ?? 0, name: v.name, value: v.value ?? '' })
            setVarOpen(false)
            void reload()
          } catch (e) { message.error((e as Error).message) }
        }}>
          <Form.Item name="name" label="نام" rules={[{ required: true }]}><Input dir="ltr" placeholder="dns_primary" /></Form.Item>
          <Form.Item name="scope" label="سطح"><Select options={Object.entries(scopeLabel).map(([value, label]) => ({ value, label }))} /></Form.Item>
          {scope === 'group' && <Form.Item name="scope_id" label="گروه" rules={[{ required: true }]}><Select options={(groups.data?.items ?? []).map((g) => ({ value: g.id, label: g.name }))} /></Form.Item>}
          {scope === 'device' && <Form.Item name="scope_id" label="دستگاه" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={(devices.data?.items ?? []).map((d) => ({ value: d.id, label: d.name }))} /></Form.Item>}
          <Form.Item name="value" label="مقدار"><Input dir="ltr" /></Form.Item>
        </Form>
      </Modal>
    </>
  )
}
