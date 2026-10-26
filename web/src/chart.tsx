import { faDateTime, faNum } from './format'
import { brand } from './theme'

type Series = { label: string; color: string; values: number[] }

// LineChart draws percentage series (0–100) over time without a charting
// library.
export function LineChart({ times, series, height = 180 }: { times: string[]; series: Series[]; height?: number }) {
  const W = 600
  const H = height
  const pad = { l: 34, r: 8, t: 8, b: 22 }
  const n = times.length
  if (n < 2) return <div style={{ color: '#888', padding: 24, textAlign: 'center' }}>هنوز داده‌ی کافی ثبت نشده است.</div>
  const x = (i: number) => pad.l + (i * (W - pad.l - pad.r)) / (n - 1)
  const y = (v: number) => pad.t + (1 - Math.max(0, Math.min(100, v)) / 100) * (H - pad.t - pad.b)
  const ticks = [0, 25, 50, 75, 100]
  return (
    <div>
      <svg viewBox={`0 0 ${W} ${H}`} style={{ width: '100%', height: 'auto', direction: 'ltr' }} role="img">
        {ticks.map((t) => (
          <g key={t}>
            <line x1={pad.l} x2={W - pad.r} y1={y(t)} y2={y(t)} stroke="#EEE" />
            <text x={pad.l - 6} y={y(t) + 4} fontSize="10" textAnchor="end" fill="#888">{faNum(t)}</text>
          </g>
        ))}
        {series.map((s) => (
          <polyline key={s.label} fill="none" stroke={s.color} strokeWidth="1.8" strokeLinejoin="round"
            points={s.values.map((v, i) => `${x(i)},${y(v)}`).join(' ')} />
        ))}
        <text x={pad.l} y={H - 6} fontSize="10" fill="#888">{faDateTime(times[0])}</text>
        <text x={W - pad.r} y={H - 6} fontSize="10" fill="#888" textAnchor="end">{faDateTime(times[n - 1])}</text>
      </svg>
      <div style={{ display: 'flex', gap: 16, justifyContent: 'center', fontSize: 12 }}>
        {series.map((s) => (
          <span key={s.label}><span style={{ display: 'inline-block', width: 10, height: 10, borderRadius: 2, background: s.color, marginInlineEnd: 4 }} />{s.label}</span>
        ))}
      </div>
    </div>
  )
}

export const chartColors = { cpu: brand.primary, mem: '#3B6FB6', disk: '#7A7A7A' }
