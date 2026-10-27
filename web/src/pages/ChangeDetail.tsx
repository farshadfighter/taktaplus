import { useEffect, useState } from 'react'
import { Alert, Button, Card, Col, Descriptions, Input, Modal, Row, Space, Spin, Table, Timeline, Typography, App as AntApp } from 'antd'
import { Link, useParams } from 'react-router'
import { api, type Change, type ChangeApproval, type ChangeTarget } from '../api'
import { useAuth, can } from '../auth'
import { changeStatusTag, kindLabel, targetStatusTag } from '../changeui'
import { faDateTime, faNum } from '../format'
import { PageHeader } from '../ui'
import { useFetch } from '../useFetch'

type Detail = { change: Change; targets: ChangeTarget[]; approvals: ChangeApproval[] }

const runModeText = (c: Change) =>
  c.run_mode === 'now' ? `فوری (دلیل: ${c.urgent_reason})` : c.run_mode === 'at' ? `در ${faDateTime(c.run_at)}` : 'در پنجره‌ی نگهداری'

export default function ChangeDetail() {
  const { id } = useParams()
  const { message } = AntApp.useApp()
  const { user } = useAuth()
  const { data, loading, error, reload, setData } = useFetch<Detail>(`/changes/${id}`)
  const [prompt, setPrompt] = useState<null | { action: string; title: string; field: string; required: boolean }>(null)
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)

  // Follow a change while it runs.
  useEffect(() => {
    if (data?.change.status !== 'running' && data?.change.status !== 'approved') return
    const t = setInterval(() => void reload(), 5000)
    return () => clearInterval(t)
  }, [data?.change.status, reload])

  if (loading && !data) return <Spin />
  if (error && !data) return <Alert type="error" showIcon title={error} />
  if (!data) return null
  const { change: c, targets, approvals } = data
  const mine = c.created_by === user?.id
  const canApprove = can(user, 'changes.approve') && !mine && c.status === 'pending' && !approvals.some((a) => a.username === user?.username)
  const canCreate = can(user, 'changes.create')

  const act = async (action: string, body: Record<string, string> = {}) => {
    setBusy(true)
    try {
      setData(await api.post<Detail>(`/changes/${id}/${action}`, body))
      setPrompt(null)
      setText('')
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Typography.Text type="secondary"><Link to="/changes">درخواست‌های تغییر</Link> / #{faNum(c.id)}</Typography.Text>
      <PageHeader title={<Space wrap>{c.title}{changeStatusTag(c.status)}</Space>} subtitle={kindLabel[c.kind]}
        extra={
          <>
            {c.status === 'draft' && canCreate && <Button type="primary" loading={busy} onClick={() => void act('submit')}>ارسال برای تأیید</Button>}
            {canApprove && <Button type="primary" onClick={() => setPrompt({ action: 'approve', title: 'تأیید تغییر', field: 'comment', required: false })}>تأیید</Button>}
            {canApprove && <Button danger onClick={() => setPrompt({ action: 'reject', title: 'رد تغییر', field: 'comment', required: true })}>رد</Button>}
            {(c.status === 'approved' || c.status === 'pending' || c.status === 'draft') && can(user, 'changes.approve') && c.run_mode !== 'now' && (
              <Button onClick={() => setPrompt({ action: 'run-now', title: 'اجرای فوری خارج از پنجره', field: 'reason', required: true })}>اجرای فوری</Button>
            )}
            {['draft', 'pending', 'approved', 'running'].includes(c.status) && canCreate && (
              <Button danger loading={busy} onClick={() => void act('cancel')}>لغو</Button>
            )}
          </>
        } />
      {c.status === 'pending' && mine && <Alert type="info" showIcon style={{ marginBottom: 16 }} title="درخواست‌دهنده نمی‌تواند تغییر خودش را تأیید کند؛ منتظر تأییدکننده بمانید." />}
      {c.status === 'approved' && (
        <Alert type="info" showIcon style={{ marginBottom: 16 }}
          title={c.next_run_at ? `اجرا ${faDateTime(c.next_run_at)} آغاز می‌شود.` : 'در هفته‌ی آینده پنجره‌ی نگهداری مناسبی برای همه‌ی دستگاه‌ها پیدا نشد.'} />
      )}
      <Row gutter={[16, 16]}>
        <Col xs={24} xl={16}>
          <Card title="دستگاه‌ها و پیش‌نمایش">
            <Table<ChangeTarget> rowKey="id" size="small" pagination={false} dataSource={targets} scroll={{ x: 700 }}
              expandable={{
                expandedRowRender: (t) => (
                  <Space orientation="vertical" style={{ width: '100%' }}>
                    <Typography.Text type="secondary">پیش‌نمایش</Typography.Text>
                    <pre dir="ltr" style={{ margin: 0, background: '#FAFAFA', padding: 8, borderRadius: 6, fontSize: 12, whiteSpace: 'pre-wrap' }}>{t.preview}</pre>
                    {t.output && <>
                      <Typography.Text type="secondary">خروجی</Typography.Text>
                      <pre dir="ltr" style={{ margin: 0, background: '#1F1F1F', color: '#EEE', padding: 8, borderRadius: 6, fontSize: 12, maxHeight: 360, overflow: 'auto', whiteSpace: 'pre-wrap' }}>{t.output}</pre>
                    </>}
                  </Space>
                ),
                defaultExpandedRowKeys: targets.length <= 3 ? targets.map((t) => t.id) : [],
              }}
              columns={[
                { title: 'ترتیب', render: (_, t) => faNum(t.position + 1) + (t.position < c.canary ? ' (آزمایشی)' : ''), width: 110 },
                { title: 'دستگاه', render: (_, t) => <Link to={`/devices/${t.device_id}`}>{t.device_name}</Link> },
                { title: 'وضعیت', render: (_, t) => targetStatusTag(t.status) },
                { title: 'خطا', render: (_, t) => t.error && <Typography.Text type="danger" style={{ fontSize: 12 }}>{t.error}</Typography.Text> },
                { title: 'بکاپ', render: (_, t) => (t.pre_backup_id ? <Link to={`/backups/${t.pre_backup_id}`}>#{faNum(t.pre_backup_id)}</Link> : '—') },
              ]} />
          </Card>
        </Col>
        <Col xs={24} xl={8}>
          <Card title="مشخصات" style={{ marginBottom: 16 }}>
            <Descriptions column={1} size="small">
              <Descriptions.Item label="درخواست‌دهنده">{c.creator}</Descriptions.Item>
              <Descriptions.Item label="زمان اجرا">{runModeText(c)}</Descriptions.Item>
              <Descriptions.Item label="اجرا">{faNum(c.canary)} دستگاه آزمایشی، هم‌زمانی {faNum(c.concurrency)}، توقف پس از {faNum(c.max_failures + 1)} خطا</Descriptions.Item>
              <Descriptions.Item label="بکاپ قبل از اجرا">{c.backup_first ? 'بله' : 'خیر'}</Descriptions.Item>
              <Descriptions.Item label="تأیید لازم">{c.required_approvals ? faNum(c.required_approvals) : 'ندارد'}</Descriptions.Item>
              {c.description && <Descriptions.Item label="توضیح">{c.description}</Descriptions.Item>}
            </Descriptions>
          </Card>
          <Card title="تاریخچه">
            <Timeline items={[
              { children: `ساخته شد توسط ${c.creator} · ${faDateTime(c.created_at)}` },
              ...(c.submitted_at ? [{ children: `ارسال برای تأیید · ${faDateTime(c.submitted_at)}` }] : []),
              ...approvals.map((a) => ({
                color: a.decision === 'approve' ? 'green' : 'red',
                children: <span>{a.decision === 'approve' ? 'تأیید' : 'رد'} توسط {a.username} · {faDateTime(a.at)}{a.comment && <div style={{ color: '#666' }}>{a.comment}</div>}</span>,
              })),
              ...(c.started_at ? [{ color: 'blue', children: `شروع اجرا · ${faDateTime(c.started_at)}` }] : []),
              ...(c.finished_at ? [{ color: c.status === 'succeeded' ? 'green' : 'red', children: <span>پایان: {changeStatusTag(c.status)} · {faDateTime(c.finished_at)}</span> }] : []),
            ]} />
          </Card>
        </Col>
      </Row>
      <Modal open={prompt !== null} title={prompt?.title} okText="ثبت" cancelText="انصراف" confirmLoading={busy}
        okButtonProps={{ disabled: !!prompt?.required && !text.trim(), danger: prompt?.action === 'reject' }}
        onCancel={() => setPrompt(null)} onOk={() => prompt && void act(prompt.action, { [prompt.field]: text })}>
        <Input.TextArea rows={3} value={text} onChange={(e) => setText(e.target.value)}
          placeholder={prompt?.field === 'reason' ? 'دلیل اجرای خارج از پنجره‌ی نگهداری' : 'توضیح (اختیاری برای تأیید)'} />
      </Modal>
    </>
  )
}
