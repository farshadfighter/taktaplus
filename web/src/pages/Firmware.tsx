import { useMemo, useState } from 'react'
import { Alert, Card, Select, Space, Table, Tag, Typography } from 'antd'
import { api, type DeviceRow, type RepoPackage } from '../api'
import { ChangeForm } from '../changeui'
import { Mono, PageHeader, kindLabel } from '../ui'
import { useFetch } from '../useFetch'

type PathResult = { current: string; path: RepoPackage[] }

export default function Firmware() {
  const images = useFetch<{ items: RepoPackage[] }>('/packages?type=firmware')
  const devices = useFetch<{ items: DeviceRow[] }>('/devices')
  const [version, setVersion] = useState<string | null>(null)
  const [paths, setPaths] = useState<Record<number, PathResult | string>>({})
  const versions = useMemo(() => Array.from(new Set((images.data?.items ?? []).map((p) => p.version))).sort((a, b) => b.localeCompare(a, undefined, { numeric: true })), [images.data])

  const check = async (v: string) => {
    setVersion(v)
    const out: Record<number, PathResult | string> = {}
    await Promise.all((devices.data?.items ?? []).map(async (d) => {
      try {
        out[d.id] = await api.get<PathResult>(`/devices/${d.id}/firmware-path?version=${encodeURIComponent(v)}`)
      } catch (e) {
        out[d.id] = (e as Error).message
      }
    }))
    setPaths(out)
  }
  const eligible = (d: DeviceRow) => !!version && typeof paths[d.id] === 'object'

  return (
    <>
      <PageHeader title="ارتقای فرم‌ور" subtitle="مسیر ارتقای مرحله‌ای از روی Imageهای مخزن و فهرست «ارتقا از» هر Image محاسبه می‌شود." />
      {images.data && images.data.items.length === 0 && <Alert type="warning" showIcon style={{ marginBottom: 16 }} title="هیچ Image فرم‌وری در مخزن نیست." />}
      <Card title="نسخه‌ی هدف" style={{ marginBottom: 16 }}>
        <Select style={{ width: 220 }} placeholder="انتخاب نسخه" value={version} onChange={(v) => void check(v)} options={versions.map((v) => ({ value: v, label: v }))} />
        {version && (
          <Table<DeviceRow> rowKey="id" size="small" pagination={false} style={{ marginTop: 16 }} dataSource={devices.data?.items ?? []} scroll={{ x: 600 }}
            columns={[
              { title: 'دستگاه', render: (_, d) => `${d.name} · ${kindLabel(d.kind)} ${d.model}` },
              { title: 'فرم‌ور فعلی', render: (_, d) => <Mono>{d.firmware || '—'}</Mono> },
              {
                title: 'مسیر', render: (_, d) => {
                  const p = paths[d.id]
                  if (!p) return '…'
                  if (typeof p === 'string') return <Typography.Text type="secondary" style={{ fontSize: 12 }}>{p}</Typography.Text>
                  return <Space wrap size={4}><Mono>{p.current}</Mono>{p.path.map((s) => <span key={s.id}>← <Tag color="blue">{s.version}</Tag></span>)}</Space>
                },
              },
            ]} />
        )}
      </Card>
      {version && (
        <ChangeForm kind="firmware" defaultTitle={`ارتقای فرم‌ور به ${version}`} spec={() => ({ version })} filterDevice={eligible}>
          <Alert type="info" showIcon style={{ marginBottom: 16 }}
            title="پیش از ارتقا سلامت دستگاه (CPU و حافظه) بررسی و بکاپ گرفته می‌شود؛ پس از هر مرحله تا بالا آمدن دستگاه با نسخه‌ی جدید صبر می‌شود. در HA فقط یک عضو را انتخاب کنید." />
        </ChangeForm>
      )}
    </>
  )
}
