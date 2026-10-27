import { useState } from 'react'
import { Button, Card, Checkbox, Form, Input, InputNumber, Modal, Popconfirm, Select, Switch, Table, Tag, TimePicker, Typography, App as AntApp } from 'antd'
import dayjs from 'dayjs'
import { api, type ApprovalPolicy, type Group, type MaintWindow } from '../api'
import { useCan } from '../auth'
import { faNum } from '../format'
import { PageHeader } from '../ui'
import { useFetch } from '../useFetch'

const days = ['یکشنبه', 'دوشنبه', 'سه‌شنبه', 'چهارشنبه', 'پنجشنبه', 'جمعه', 'شنبه']

export default function Maintenance() {
  const { message } = AntApp.useApp()
  const canEdit = useCan('settings.manage')
  const { data, reload } = useFetch<{ windows: MaintWindow[]; policies: ApprovalPolicy[] }>('/maintenance')
  const groups = useFetch<{ items: Group[] }>('/groups')
  const [editing, setEditing] = useState<Partial<MaintWindow> | null>(null)
  const [form] = Form.useForm()
  const groupName = (id: number | null) => (id ? groups.data?.items.find((g) => g.id === id)?.name ?? id : 'همه‌ی دستگاه‌ها')
  const def = data?.policies.find((p) => p.group_id === null)?.required ?? 1

  const setPolicy = async (group: number | null, required: number | null) => {
    try {
      await api.post('/maintenance/policies', { group_id: group, required })
      void reload()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  return (
    <>
      <PageHeader title="پنجره‌های نگهداری و تأیید" subtitle="تغییرات تأییدشده فقط در پنجره‌ی نگهداری دستگاه اجرا می‌شوند، مگر با «اجرای فوری»." />
      <Card title="پنجره‌های نگهداری" style={{ marginBottom: 16 }} extra={canEdit && <Button type="primary" onClick={() => {
        setEditing({}); form.resetFields(); form.setFieldsValue({ weekdays: [4], start: dayjs('22:00', 'HH:mm'), minutes: 240, enabled: true })
      }}>+ پنجره</Button>}>
        <Typography.Paragraph type="secondary">دستگاهی که هیچ پنجره‌ای شامل آن نشود، بلافاصله پس از تأیید تغییر می‌کند. ساعت‌ها به وقت سرور است.</Typography.Paragraph>
        <Table<MaintWindow> rowKey="id" size="small" pagination={false} dataSource={data?.windows ?? []} locale={{ emptyText: 'پنجره‌ای تعریف نشده' }}
          columns={[
            { title: 'نام', dataIndex: 'name' },
            { title: 'دامنه', render: (_, w) => groupName(w.group_id) },
            { title: 'روزها', render: (_, w) => w.weekdays.map((d) => days[d]).join('، ') },
            { title: 'زمان', render: (_, w) => `${w.start_time} به مدت ${faNum(w.minutes / 60)} ساعت` },
            { title: 'وضعیت', render: (_, w) => (w.enabled ? <Tag color="green">فعال</Tag> : <Tag>غیرفعال</Tag>) },
            ...(canEdit ? [{
              title: '', render: (_: unknown, w: MaintWindow) => (
                <>
                  <Button size="small" type="link" onClick={() => { setEditing(w); form.setFieldsValue({ ...w, start: dayjs(w.start_time, 'HH:mm') }) }}>ویرایش</Button>
                  <Popconfirm title="پنجره حذف شود؟" okText="حذف" cancelText="انصراف" onConfirm={async () => { await api.del(`/maintenance/windows/${w.id}`); void reload() }}>
                    <Button size="small" type="link" danger>حذف</Button>
                  </Popconfirm>
                </>
              ),
            }] : []),
          ]} />
      </Card>
      <Card title="سیاست تأیید">
        <Typography.Paragraph type="secondary">هر تغییر به بیشترین تعداد تأیید لازم میان دستگاه‌هایش نیاز دارد. درخواست‌دهنده نمی‌تواند تغییر خودش را تأیید کند.</Typography.Paragraph>
        <Table rowKey="key" size="small" pagination={false}
          dataSource={[{ key: 'default', group_id: null as number | null, name: 'پیش‌فرض (همه‌ی گروه‌ها)', required: def, custom: true },
            ...(groups.data?.items ?? []).map((g) => {
              const p = data?.policies.find((x) => x.group_id === g.id)
              return { key: String(g.id), group_id: g.id as number | null, name: g.name, required: p?.required ?? def, custom: !!p }
            })]}
          columns={[
            { title: 'گروه', dataIndex: 'name' },
            {
              title: 'تأیید لازم', render: (_, r) => (
                <Select disabled={!canEdit} value={r.custom ? r.required : -1} style={{ width: 200 }} onChange={(v) => void setPolicy(r.group_id, v === -1 ? null : v)}
                  options={[...(r.group_id ? [{ value: -1, label: `مثل پیش‌فرض (${faNum(def)})` }] : []), { value: 0, label: 'بدون تأیید' }, { value: 1, label: 'یک تأییدکننده' }, { value: 2, label: 'دو تأییدکننده' }]} />
              ),
            },
          ]} />
      </Card>
      <Modal open={editing !== null} title={editing?.id ? 'ویرایش پنجره' : 'پنجره‌ی نگهداری جدید'} okText="ذخیره" cancelText="انصراف" onCancel={() => setEditing(null)} onOk={() => form.submit()}>
        <Form form={form} layout="vertical" requiredMark={false} onFinish={async (v) => {
          const body = { name: v.name, group_id: v.group_id ?? null, weekdays: v.weekdays, start_time: (v.start as dayjs.Dayjs).format('HH:mm'), minutes: v.minutes, enabled: v.enabled }
          try {
            if (editing?.id) await api.put(`/maintenance/windows/${editing.id}`, body)
            else await api.post('/maintenance/windows', body)
            setEditing(null)
            void reload()
          } catch (e) { message.error((e as Error).message) }
        }}>
          <Form.Item name="name" label="نام" rules={[{ required: true }]}><Input placeholder="شب‌های پنجشنبه" /></Form.Item>
          <Form.Item name="group_id" label="دامنه"><Select allowClear placeholder="همه‌ی دستگاه‌ها" options={(groups.data?.items ?? []).map((g) => ({ value: g.id, label: g.name }))} /></Form.Item>
          <Form.Item name="weekdays" label="روزها" rules={[{ required: true }]}><Checkbox.Group options={days.map((d, i) => ({ value: i, label: d }))} /></Form.Item>
          <Form.Item name="start" label="شروع" rules={[{ required: true }]}><TimePicker format="HH:mm" minuteStep={15} /></Form.Item>
          <Form.Item name="minutes" label="مدت (دقیقه)"><InputNumber min={15} max={1440} step={15} /></Form.Item>
          <Form.Item name="enabled" label="فعال" valuePropName="checked"><Switch /></Form.Item>
        </Form>
      </Modal>
    </>
  )
}
