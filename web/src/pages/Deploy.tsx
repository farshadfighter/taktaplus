import { useState } from 'react'
import { Card, Checkbox, Radio, Select, Space, Switch, Table, Tag, Typography } from 'antd'
import type { RepoPackage } from '../api'
import { ChangeForm } from '../changeui'
import { faDate } from '../format'
import { Mono, PageHeader } from '../ui'
import { useFetch } from '../useFetch'
import { typeLabel } from './Packages'

const fgTypes = ['ips_db', 'ips_engine', 'av_db', 'av_engine', 'appctrl_db', 'botnet_db', 'isdb', 'ot_db']

export default function Deploy() {
  const pkgs = useFetch<{ items: RepoPackage[] }>('/packages')
  const [mode, setMode] = useState<'latest' | 'packages'>('latest')
  const [latest, setLatest] = useState<string[]>(['ips_db', 'av_db'])
  const [selected, setSelected] = useState<string[]>([])
  const [method, setMethod] = useState('auto')
  const [verify, setVerify] = useState(true)
  const sigs = (pkgs.data?.items ?? []).filter((p) => p.type !== 'firmware')
  const types = Array.from(new Set([...fgTypes, ...sigs.map((p) => p.type)]))

  return (
    <>
      <PageHeader title="استقرار سیگنیچر" subtitle="نصب پکیج‌های مخزن روی دستگاه‌ها؛ برای هر دستگاه فقط پکیج‌های سازگار نصب می‌شوند." />
      <ChangeForm kind="signatures" defaultTitle="بروزرسانی سیگنیچرها"
        spec={() => (mode === 'latest' ? (latest.length ? { latest, method, verify } : null) : selected.length ? { packages: selected, method, verify } : null)}>
        <Card title="پکیج‌ها" style={{ marginBottom: 16 }}>
          <Radio.Group value={mode} onChange={(e) => setMode(e.target.value)} style={{ marginBottom: 12 }}
            options={[{ value: 'latest', label: 'جدیدترین نسخه‌ی سازگار از هر نوع' }, { value: 'packages', label: 'پکیج‌های مشخص' }]} />
          {mode === 'latest' ? (
            <Checkbox.Group value={latest} onChange={(v) => setLatest(v as string[])}
              options={types.map((t) => ({ value: t, label: typeLabel[t] ?? t }))} />
          ) : (
            <Table<RepoPackage> rowKey="sha256" size="small" loading={pkgs.loading} dataSource={sigs} pagination={{ pageSize: 10, hideOnSinglePage: true }}
              rowSelection={{ selectedRowKeys: selected, onChange: (k) => setSelected(k as string[]) }} scroll={{ x: 600 }}
              columns={[
                { title: 'نوع', render: (_, p) => `${p.platform === 'fortigate' ? 'FortiGate' : 'FortiWeb'} · ${typeLabel[p.type] ?? p.type}` },
                { title: 'نسخه', render: (_, p) => <Mono>{p.version}</Mono> },
                { title: 'مدل‌ها', render: (_, p) => p.models.join('، ') },
                { title: 'انتشار', render: (_, p) => faDate(p.released_at) },
                { title: '', render: (_, p) => (p.verified ? <Tag color="green">امضاشده</Tag> : <Tag>بدون امضا</Tag>) },
              ]} />
          )}
          <Space wrap size={24} style={{ marginTop: 16 }}>
            <span>روش نصب:{' '}
              <Select value={method} onChange={setMethod} style={{ width: 230 }} options={[
                { value: 'auto', label: 'خودکار (API، سپس TFTP، سپس FTP)' }, { value: 'api', label: 'آپلود با REST API' },
                { value: 'tftp', label: 'دریافت دستگاه از TFTP داخلی' }, { value: 'ftp', label: 'دریافت دستگاه از FTP داخلی' },
              ]} />
            </span>
            <span><Switch checked={verify} onChange={setVerify} /> بررسی نسخه پس از نصب</span>
          </Space>
          <Typography.Paragraph type="secondary" style={{ fontSize: 12, marginTop: 12, marginBottom: 0 }}>
            دستگاه باید قرارداد FortiGuard معتبر برای همان سرویس داشته باشد؛ TaktaPlus این را دور نمی‌زند.
          </Typography.Paragraph>
        </Card>
      </ChangeForm>
    </>
  )
}
