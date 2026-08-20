import { useEffect, useMemo, useRef, useState, type ChangeEvent } from 'react'
import { evidenceDownloadUrl } from '../api/client'
import { PageError, PageSkeleton } from '../components/AsyncState'
import {
  useDashboard,
  useDeleteEvidence,
  useEvidences,
  useEvidenceGroups,
  useRebuildEvidenceGroups,
  useSetItemStatus,
  useUploadEvidence,
} from '../hooks/api'
import { useToast } from '../hooks/useToast'
import { friendlyError } from '../lib/friendlyError'
import { confidenceFor, formatDate } from '../lib/format'
import type { DashboardItem, Evidence, EvidenceGroup } from '../types'

type GroupFilter = 'all' | 'review' | 'high' | 'manual'

interface GroupConfidence {
  key: 'manual' | 'high' | 'review' | 'empty'
  label: string
}

const EMPTY_GROUPS: EvidenceGroup[] = []

function groupConfidence(value?: string): GroupConfidence {
  const raw = String(value || '').toLowerCase()
  if (raw.includes('manual')) return { key: 'manual', label: 'Agregada por ti' }
  if (raw.includes('confirm') || raw.includes('high') || raw.includes('alta')) {
    return { key: 'high', label: 'Confirmada' }
  }
  if (raw.includes('suggest') || raw.includes('review') || raw.includes('revis')) {
    return { key: 'review', label: 'Por revisar' }
  }
  return { key: 'review', label: 'Por revisar' }
}

export function Evidences() {
  const dashboardQuery = useDashboard()
  const dashboard = dashboardQuery.data
  const activeFichaId = dashboard?.activeFichaId
  const { data: evidenceGroups } = useEvidenceGroups(activeFichaId)
  const { data: evidences } = useEvidences(activeFichaId)
  const rebuildGroups = useRebuildEvidenceGroups()
  const uploadEvidence = useUploadEvidence()
  const deleteEvidence = useDeleteEvidence()
  const setItemStatus = useSetItemStatus()
  const toast = useToast()

  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<GroupFilter>('all')
  const [formatFilter, setFormatFilter] = useState('all')
  const [expanded, setExpanded] = useState(false)
  const [preview, setPreview] = useState<Evidence | null>(null)
  const [uploadItemCode, setUploadItemCode] = useState('')
  const [selectedEvidenceIds, setSelectedEvidenceIds] = useState<Set<string>>(new Set())
  const fileInputRef = useRef<HTMLInputElement>(null)

  const groups = evidenceGroups ?? EMPTY_GROUPS
  const groupedEvidences = useMemo(() => {
    const seen = new Set<string>()
    return groups.flatMap((group) => group.evidences || []).filter((evidence) => {
      if (!evidence.id || seen.has(evidence.id)) return false
      seen.add(evidence.id)
      return true
    })
  }, [groups])
  const flatEvidences = evidences?.length ? evidences : groupedEvidences

  if (dashboardQuery.isLoading) return <PageSkeleton label="Cargando galería de evidencias" />
  if (dashboardQuery.isError || !dashboard) return <PageError message="No pudimos cargar las evidencias de la ficha activa." action={<button className="button" onClick={() => dashboardQuery.refetch()}>Reintentar</button>} />

  const dashboardItems = dashboard?.items || []
  const relatedItems = dashboardItems.filter((item) => item.evidenceCount)

  const handleRebuild = () => {
    if (!activeFichaId) return
    rebuildGroups.mutate(activeFichaId, {
      onSuccess: () => toast('Agrupación de evidencias actualizada.'),
      onError: (error) => toast(friendlyError(error.message), true),
    })
  }

  const handlePickUpload = () => {
    fileInputRef.current?.click()
  }

  const handleFileChange = (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    event.target.value = ''
    if (!file || !activeFichaId) return
    const form = new FormData()
    form.append('file', file)
    form.append('fichaId', activeFichaId)
    if (uploadItemCode) form.append('itemCode', uploadItemCode)
    uploadEvidence.mutate(form, {
      onSuccess: (result) => toast(`Evidencia "${result.name || file.name}" añadida.`),
      onError: (error) => toast(friendlyError(error.message), true),
    })
  }

  const handleDelete = (evidenceId: string) => {
    if (!window.confirm('¿Eliminar esta evidencia? Esta acción no se puede deshacer.')) return
    deleteEvidence.mutate(evidenceId, {
      onSuccess: () => {
        toast('Evidencia eliminada.')
        setPreview(null)
      },
      onError: (error) => toast(friendlyError(error.message), true),
    })
  }

  const handleMarkRelatedYes = async () => {
    if (!activeFichaId || !relatedItems.length || setItemStatus.isPending) return
    try {
      await Promise.all(
        relatedItems
          .filter((item) => item.status !== 'SI')
          .map((item) => setItemStatus.mutateAsync({ itemCode: item.itemCode, fichaId: activeFichaId, status: 'SI' })),
      )
      toast('Marcamos como Sí las tareas que ya tienen evidencia asociada.')
    } catch (error) {
      toast(friendlyError(error instanceof Error ? error.message : String(error)), true)
    }
  }

  const previewIndex = preview ? flatEvidences.findIndex((evidence) => evidence.id === preview.id) : -1
  const showPrevious = () => {
    if (previewIndex > 0) setPreview(flatEvidences[previewIndex - 1])
  }
  const showNext = () => {
    if (previewIndex >= 0 && previewIndex < flatEvidences.length - 1) setPreview(flatEvidences[previewIndex + 1])
  }

  return (
    <div className="grid main-grid">
      <div className="grid">
        <EvidenceGallery
          groups={groups}
          query={query}
          onQueryChange={setQuery}
          filter={filter}
          onFilterChange={setFilter}
          formatFilter={formatFilter}
          onFormatFilterChange={setFormatFilter}
          expanded={expanded}
          onToggleExpanded={() => setExpanded((current) => !current)}
          onRebuild={handleRebuild}
          rebuilding={rebuildGroups.isPending}
          onPreview={setPreview}
        />
        <EvidenceMiniatures
          evidences={flatEvidences}
          selectedIds={selectedEvidenceIds}
          onSelectionChange={setSelectedEvidenceIds}
          onPreview={setPreview}
        />
        <section className="card">
          <div className="card-pad">
            <div className="side-title">
              <div>
                <h3>Archivos relacionados</h3>
                <p className="helper" style={{ marginTop: 5 }}>
                  Estas son las tareas que ya tienen una evidencia local asociada.
                </p>
              </div>
              <div className="inline evidence-related-actions">
                <span className="badge">{relatedItems.length} con evidencia</span>
                <button className="button ghost small" type="button" onClick={handleMarkRelatedYes} disabled={!relatedItems.length || setItemStatus.isPending}>
                  {setItemStatus.isPending ? 'Guardando…' : 'Marcar relacionadas como Sí'}
                </button>
              </div>
            </div>
            <div className="task-list" style={{ marginTop: 14 }}>
              {relatedItems.length ? (
                relatedItems.map((item) => (
                  <Task key={item.itemCode} item={item} fichaId={activeFichaId} onPreview={setPreview} />
                ))
              ) : (
                <div className="empty">Aún no hay archivos asociados a esta ficha.</div>
              )}
            </div>
          </div>
        </section>
      </div>
      <aside className="side-stack">
        <section className="card">
          <div className="card-pad">
            <h3>Agregar una evidencia</h3>
            <p className="helper" style={{ marginTop: 8 }}>
              Puedes subir una captura o documento que ya tengas en este equipo.
            </p>
            <div className="manual-upload">
              <label htmlFor="manual-evidence-item">Relacionar con una actividad (opcional)</label>
              <select
                id="manual-evidence-item"
                value={uploadItemCode}
                onChange={(event) => setUploadItemCode(event.target.value)}
              >
                <option value="">Evidencia general de la ficha</option>
                {dashboardItems.map((item) => (
                  <option key={item.itemCode} value={item.itemCode}>
                    {item.itemCode} · {item.description}
                  </option>
                ))}
              </select>
              <input
                ref={fileInputRef}
                id="manual-evidence-file"
                type="file"
                accept=".png,.jpg,.jpeg,.pdf,.html"
                hidden
                onChange={handleFileChange}
              />
              <button
                className="button secondary"
                type="button"
                disabled={uploadEvidence.isPending || !activeFichaId}
                onClick={handlePickUpload}
              >
                Subir evidencia
              </button>
              <p className="helper">Acepta PNG, JPG, PDF o HTML.</p>
            </div>
          </div>
        </section>
      </aside>
      {preview && <PreviewModal evidence={preview} index={previewIndex} total={flatEvidences.length} onPrevious={showPrevious} onNext={showNext} onClose={() => setPreview(null)} onDelete={handleDelete} />}
    </div>
  )
}

interface EvidenceGalleryProps {
  groups: EvidenceGroup[]
  query: string
  onQueryChange: (value: string) => void
  filter: GroupFilter
  onFilterChange: (value: GroupFilter) => void
  formatFilter: string
  onFormatFilterChange: (value: string) => void
  expanded: boolean
  onToggleExpanded: () => void
  onRebuild: () => void
  rebuilding: boolean
  onPreview: (evidence: Evidence) => void
}

function EvidenceMiniatures({
  evidences,
  selectedIds,
  onSelectionChange,
  onPreview,
}: {
  evidences: Evidence[]
  selectedIds: Set<string>
  onSelectionChange: (ids: Set<string>) => void
  onPreview: (evidence: Evidence) => void
}) {
  const toggle = (id: string) => {
    const next = new Set(selectedIds)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    onSelectionChange(next)
  }

  const selectAll = () => onSelectionChange(new Set(evidences.map((evidence) => evidence.id)))
  const clearAll = () => onSelectionChange(new Set())

  return (
    <section className="card evidence-miniatures">
      <div className="card-pad">
        <div className="side-title">
          <div>
            <div className="eyebrow">Revisión visual</div>
            <h3 style={{ marginTop: 7 }}>Miniaturas seleccionables</h3>
            <p className="helper" style={{ marginTop: 5 }}>
              Selecciona las evidencias que quieres revisar juntas. Esta selección es local y no altera la agrupación del reporte.
            </p>
          </div>
          <span className="badge">{selectedIds.size} seleccionadas</span>
        </div>
        <div className="miniature-toolbar">
          <span className="helper">{evidences.length} archivos en esta ficha</span>
          <div className="inline">
            <button className="button ghost small" type="button" onClick={selectAll} disabled={!evidences.length}>Seleccionar todas</button>
            <button className="button ghost small" type="button" onClick={clearAll} disabled={!selectedIds.size}>Limpiar</button>
          </div>
        </div>
        {evidences.length ? (
          <div className="evidence-miniature-grid">
            {evidences.map((evidence) => {
              const format = String(evidence.format || '').toLowerCase()
              const image = format.includes('png') || format.includes('jpg') || format.includes('jpeg') || format.includes('webp')
              const selected = selectedIds.has(evidence.id)
              return (
                <article key={evidence.id} className={`evidence-miniature${selected ? ' selected' : ''}`}>
                  <div className="evidence-miniature-select" onClick={() => toggle(evidence.id)}>
                    <span className="evidence-miniature-preview" role="button" tabIndex={0} onClick={(event) => { event.stopPropagation(); onPreview(evidence) }} onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ') onPreview(evidence) }}>
                      {image ? <img src={evidenceDownloadUrl(evidence.id)} alt="" loading="lazy" /> : <span className="evidence-format-icon">{format.toUpperCase() || 'FILE'}</span>}
                      <span className="evidence-select-mark" aria-hidden="true">{selected ? '✓' : ''}</span>
                    </span>
                    <span className="evidence-miniature-copy">
                      <strong>{evidence.name || 'Evidencia local'}</strong>
                      <small>{String(evidence.format || 'archivo').toUpperCase()} · {formatDate(evidence.capturedAt)}</small>
                    </span>
                  </div>
                  <button className="evidence-miniature-open" type="button" onClick={() => onPreview(evidence)}>Vista previa</button>
                </article>
              )
            })}
          </div>
        ) : (
          <div className="empty">Aún no hay miniaturas disponibles para esta ficha.</div>
        )}
      </div>
    </section>
  )
}

function EvidenceGallery({
  groups,
  query,
  onQueryChange,
  filter,
  onFilterChange,
  formatFilter,
  onFormatFilterChange,
  expanded,
  onToggleExpanded,
  onRebuild,
  rebuilding,
  onPreview,
}: EvidenceGalleryProps) {
  const normalizedQuery = query.trim().toLowerCase()

  const allFormats = useMemo(() => {
    const set = new Set<string>()
    groups.forEach((group) => {
      group.evidences.forEach((evidence) => {
        const format = String(evidence.format || '').toLowerCase()
        if (format) set.add(format)
      })
    })
    return [...set]
  }, [groups])

  const matches = groups.filter((group) => {
    const confidence = groupConfidence(group.confidence).key
    const haystack = [group.title, ...(group.itemCodes || []), group.reason].join(' ').toLowerCase()
    const formatOk =
      formatFilter === 'all' ||
      group.evidences.some((evidence) => String(evidence.format || '').toLowerCase() === formatFilter)
    return (filter === 'all' || confidence === filter) && (!normalizedQuery || haystack.includes(normalizedQuery)) && formatOk
  })

  const total = matches.reduce((sum, group) => sum + (group.evidences?.length || 0), 0)
  const visible = expanded ? matches : matches.slice(0, 6)

  return (
    <section className="card evidence-gallery">
      <div className="card-pad">
        <div className="confidence-intro">
          <div>
            <div className="eyebrow">Evidencias organizadas</div>
            <h3 style={{ marginTop: 7 }}>Galería de evidencias</h3>
            <p className="helper" style={{ marginTop: 6 }}>
              Agrupamos archivos que muestran la misma sección para evitar repeticiones en tu reporte.
            </p>
          </div>
          <button className="button ghost small" type="button" disabled={rebuilding} onClick={onRebuild}>
            Revisar agrupación
          </button>
        </div>
        <div className="gallery-controls">
          <input
            id="evidence-group-search"
            type="search"
            value={query}
            onChange={(event) => onQueryChange(event.target.value)}
            placeholder="Buscar por título o código"
            aria-label="Buscar grupos de evidencias"
          />
          <select
            id="evidence-group-filter"
            aria-label="Filtrar grupos de evidencias"
            value={filter}
            onChange={(event) => onFilterChange(event.target.value as GroupFilter)}
          >
            <option value="all">Todos los estados</option>
            <option value="review">Por revisar</option>
            <option value="high">Confirmadas</option>
            <option value="manual">Agregadas por ti</option>
          </select>
        </div>
        {allFormats.length ? (
          <div className="checklist-filter-tabs" style={{ marginTop: 10 }}>
            {['all', ...allFormats].map((value) => (
              <button
                key={value}
                type="button"
                className={`checklist-filter-tab ${formatFilter === value ? 'active' : ''}`}
                aria-pressed={formatFilter === value}
                onClick={() => onFormatFilterChange(value)}
              >
                {value === 'all' ? 'Todos los formatos' : value.toUpperCase()}
              </button>
            ))}
          </div>
        ) : null}
        <div className="gallery-summary">
          <div className="gallery-summary-stat"><strong>{matches.length}</strong><span>grupos visibles</span></div>
          <div className="gallery-summary-stat"><strong>{total}</strong><span>archivos relacionados</span></div>
          <div className="gallery-summary-stat"><strong>{groups.length}</strong><span>grupos totales</span></div>
        </div>
        <div className="evidence-gallery-grid">
          {visible.length ? (
            visible.map((group, index) => (
              <EvidenceGroupCard key={group.id || `${group.title || 'grupo'}-${index}`} group={group} onPreview={onPreview} />
            ))
          ) : (
            <div className="empty">No encontramos grupos con esos filtros.</div>
          )}
        </div>
        {matches.length > 6 && (
          <div className="gallery-more">
            <p className="helper">
              {expanded ? 'Mostrando todos los grupos filtrados.' : 'Mostrando 6 grupos para una revisión rápida.'} El reporte
              conserva todos los archivos relacionados.
            </p>
            <button className="button ghost small" type="button" onClick={onToggleExpanded}>
              {expanded ? 'Mostrar menos' : 'Mostrar todos los grupos'}
            </button>
          </div>
        )}
      </div>
    </section>
  )
}

function EvidenceGroupCard({ group, onPreview }: { group: EvidenceGroup; onPreview: (evidence: Evidence) => void }) {
  const evidence = group.evidences[0]
  const confidence = groupConfidence(group.confidence)
  const codes = group.itemCodes || []
  const itemsLabel = codes.slice(0, 5).join(' · ') + (codes.length > 5 ? ' · …' : '')
  const count = group.evidences.length

  return (
    <article className="evidence-group-card">
      <div className="evidence-group-top">
        <span className={`confidence ${confidence.key}`}>{confidence.label}</span>
        <span className="confidence-stamp">
          {count} archivo{count === 1 ? '' : 's'}
        </span>
      </div>
      <h4>{group.title || 'Evidencia agrupada'}</h4>
      <p className="helper">{itemsLabel || 'Evidencia general de la ficha'}</p>
      <div className="evidence-group-foot">
        <span className="group-reason">{group.reason || 'Misma sección del curso'}</span>
        {evidence && (
          <button className="button ghost small" type="button" onClick={() => onPreview(evidence)}>
            Vista previa
          </button>
        )}
      </div>
    </article>
  )
}

function Task({
  item,
  fichaId,
  onPreview,
}: {
  item: DashboardItem
  fichaId?: string
  onPreview: (evidence: Evidence) => void
}) {
  const toast = useToast()
  const setItemStatus = useSetItemStatus()
  const confidence = confidenceFor(item)
  const maxEvidences = Number(item.maxEvidences) || 1
  const filled = Math.min(Number(item.evidenceCount) || 0, maxEvidences)
  const current = String(item.status || 'PENDIENTE')

  const handleStatus = (status: string) => {
    if (!fichaId || status === current) return
    setItemStatus.mutate(
      { itemCode: item.itemCode, fichaId, status },
      { onError: (error) => toast(friendlyError(error.message), true) },
    )
  }

  return (
    <article className="task task-grid">
      <span className="task-code mono">{item.itemCode}</span>
      <div className="task-main">
        <div className="task-desc">{item.description}</div>
        <div className="task-meta">
          <span>{item.categoryLabel}</span>
          <span className={`confidence ${confidence.key}`} title={confidence.detail}>
            {confidence.label}
          </span>
        </div>
        <div className="task-evidences">
          {item.evidences.length ? (
            item.evidences.map((evidence) => (
              <button key={evidence.id} className="evidence-link" type="button" onClick={() => onPreview(evidence)}>
                Ver evidencia {evidence.slotNumber || 1}
              </button>
            ))
          ) : (
            <span className="helper">Aún no hay un archivo</span>
          )}
        </div>
      </div>
      <div className="task-slots">
        <div className="slot-row">
          {Array.from({ length: maxEvidences }).map((_, index) => (
            <span key={index} className={`slot-dot${index < filled ? ' filled' : ''}`} />
          ))}
        </div>
        <small>
          {filled} / {maxEvidences}
        </small>
      </div>
      <div className="status-seg-group" role="group" aria-label="Cambiar estado">
        <button
          type="button"
          className={`status-seg si ${current === 'SI' ? 'active' : ''}`}
          onClick={() => handleStatus('SI')}
        >
          Sí
        </button>
        <button
          type="button"
          className={`status-seg no ${current === 'NO' ? 'active' : ''}`}
          onClick={() => handleStatus('NO')}
        >
          No
        </button>
        <button
          type="button"
          className={`status-seg pendiente ${current === 'PENDIENTE' ? 'active' : ''}`}
          onClick={() => handleStatus('PENDIENTE')}
        >
          Pend.
        </button>
      </div>
    </article>
  )
}

function PreviewModal({
  evidence,
  index,
  total,
  onPrevious,
  onNext,
  onClose,
  onDelete,
}: {
  evidence: Evidence
  index: number
  total: number
  onPrevious: () => void
  onNext: () => void
  onClose: () => void
  onDelete: (id: string) => void
}) {
  const format = String(evidence.format || '').toLowerCase()
  const src = evidenceDownloadUrl(evidence.id)
  const title = evidence.name || 'Vista previa de evidencia'
  const closeRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null
    closeRef.current?.focus()
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
      if (event.key === 'ArrowLeft') onPrevious()
      if (event.key === 'ArrowRight') onNext()
      if (event.key !== 'Tab') return
      const dialog = closeRef.current?.closest('[role="dialog"]')
      const focusable = Array.from(dialog?.querySelectorAll<HTMLElement>('button, a[href], iframe, input, select, textarea, [tabindex]:not([tabindex="-1"])') || []).filter((element) => !element.hasAttribute('disabled'))
      if (!focusable.length) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault()
        first.focus()
      }
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => {
      document.removeEventListener('keydown', handleKeyDown)
      previous?.focus?.()
    }
  }, [onClose, onNext, onPrevious])

  return (
    <div id="evidence-modal" className="evidence-modal" role="dialog" aria-modal="true" aria-labelledby="evidence-preview-title">
      <div className="evidence-dialog">
        <div className="evidence-dialog-head">
          <h3 id="evidence-preview-title">{title}</h3>
          <button ref={closeRef} className="button ghost small" type="button" onClick={onClose}>
            Cerrar
          </button>
        </div>
        <div className="evidence-dialog-body">
          {format === 'pdf' ? (
            <iframe src={src} title={`Vista previa de ${evidence.name}`} />
          ) : format === 'html' ? (
            <iframe sandbox="allow-same-origin" src={src} title={`Vista previa de ${evidence.name}`} />
          ) : (
            <img src={src} alt={evidence.name} />
          )}
        </div>
        <div className="evidence-dialog-navigation" aria-label="Navegar evidencias">
          <button className="button ghost small" type="button" onClick={onPrevious} disabled={index <= 0}>← Anterior</button>
          <span className="helper">{index + 1} de {total}</span>
          <button className="button ghost small" type="button" onClick={onNext} disabled={index < 0 || index >= total - 1}>Siguiente →</button>
        </div>
        <div className="evidence-dialog-foot">
          <span className="helper">
            {evidence.source || 'Evidencia local'} · {formatDate(evidence.capturedAt)}
          </span>
          <a className="button secondary small" href={src} target="_blank" rel="noreferrer">
            Descargar
          </a>
          <button className="button ghost small" type="button" onClick={() => onDelete(evidence.id)}>
            Eliminar
          </button>
        </div>
      </div>
    </div>
  )
}
