export function friendlyError(message: string) {
  return String(message || '')
    .replace(/autenticación[^:]*:?\s*/i, 'No pudimos conectar con Zajuna: ')
    .replace(/selector[^.]*\.?/i, 'No encontramos la información esperada en esta página.')
    .replace(/(WAF|página bloqueada)[^.]*\.?/i, 'Zajuna bloqueó temporalmente esta consulta. Inténtalo de nuevo más tarde.')
}
