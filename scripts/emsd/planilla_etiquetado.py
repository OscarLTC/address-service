# Arma la planilla de etiquetado de la muestra real (pista C). Lee y escribe solo
# dentro de data/private/: contiene direcciones de clientes.
#
#   go run ./cmd/sampleprofile -examples 0 -out-csv data/private/emsd_propuesta.csv
#   python scripts/emsd/planilla_etiquetado.py
import csv, hashlib
from collections import defaultdict
from openpyxl import Workbook
from openpyxl.styles import Font, PatternFill, Alignment, Border, Side
from openpyxl.worksheet.datavalidation import DataValidation
from openpyxl.formatting.rule import FormulaRule
from openpyxl.comments import Comment

SRC = 'data/private/emsd_propuesta.csv'
OUT = 'data/private/etiquetado_muestra_v1.xlsx'
N = 200
PER_ACCOUNT = 8
TEST_PCT = 30

rows = list(csv.DictReader(open(SRC, encoding='utf-8')))
rows = [r for r in rows if r['ubigeo'][:4] in ('1501', '0701')]

INTEREST = {'DISTRICT_CONFLICT', 'DISTRICT_NAME_IN_TEXT', 'INTERIOR_FROM_BARE_NUMBER',
            'DISTRICT_FIELD_PARTIAL', 'MISSING_UNIT_VALUE', 'TRAILING_TEXT_MOVED_TO_REFERENCE',
            'UBIGEO_IN_TEXT', 'POSTAL_CODE_IN_TEXT', 'COUNTRY_REMOVED', 'NO_NUMBER'}

def h(s):
    return hashlib.md5(s.encode()).hexdigest()

def priority(r):
    flags = set(r['p_flags'].split())
    hard = bool(flags & INTEREST) or r['p_mz'] or r['p_lt'] or r['p_urbanizacion']
    return (0 if hard else 1, h('orden' + r['id_muestra']))

by_acc = defaultdict(list)
for r in rows:
    by_acc[r['cuenta']].append(r)
for acc in by_acc:
    by_acc[acc].sort(key=priority)

# Ronda por cuenta (tope PER_ACCOUNT); si no alcanza N, se completa sin tope.
chosen, seen = [], set()
for cap in (PER_ACCOUNT, 10**6):
    progress = True
    level = 0
    while len(chosen) < N and progress:
        progress = False
        for acc in sorted(by_acc, key=lambda a: h(a)):
            lst = by_acc[acc]
            if level < len(lst) and level < cap:
                r = lst[level]
                if r['id_muestra'] not in seen:
                    chosen.append(r); seen.add(r['id_muestra'])
                    progress = True
                    if len(chosen) >= N:
                        break
        level += 1
chosen.sort(key=lambda r: h('fila' + r['id_muestra']))

# --- estilos ---
F = 'Arial'
font = Font(name=F, size=10)
bold = Font(name=F, size=10, bold=True)
white_bold = Font(name=F, size=10, bold=True, color='FFFFFF')
title = Font(name=F, size=14, bold=True)
wrap = Alignment(wrap_text=True, vertical='top')
fill_src = PatternFill('solid', start_color='DDEBF7')    # datos de EMS-D
fill_prop = PatternFill('solid', start_color='EDEDED')   # propuesta (no editar)
fill_edit = PatternFill('solid', start_color='FFF2CC')   # editable
fill_h_src = PatternFill('solid', start_color='2F5597')
fill_h_prop = PatternFill('solid', start_color='595959')
fill_h_edit = PatternFill('solid', start_color='BF8F00')
fill_h_meta = PatternFill('solid', start_color='375623')
thin = Side(style='thin', color='BFBFBF')
border = Border(left=thin, right=thin, top=thin, bottom=thin)

wb = Workbook()
ins = wb.active
ins.title = 'Instrucciones'
ws = wb.create_sheet('Etiquetado')
res = wb.create_sheet('Resumen')

# --- hoja Etiquetado ---
cols = [
    # (encabezado, ancho, grupo, clave)
    ('ID muestra', 13, 'src', 'id_muestra'),
    ('Cuenta', 10, 'src', 'cuenta'),
    ('Dirección (EMS-D)', 46, 'src', 'direccion'),
    ('Número (campo)', 9, 'src', 'numero'),
    ('Referencia (campo)', 30, 'src', 'referencia'),
    ('Distrito (EMS-D)', 18, 'src', 'distrito'),
    ('Provincia (EMS-D)', 11, 'src', 'provincia'),
    ('Ubigeo (EMS-D)', 9, 'src', 'ubigeo'),
    ('Tipo de vía', 13, 'prop', 'p_tipo_via'),
    ('Nombre de vía', 24, 'prop', 'p_nombre_via'),
    ('Número', 8, 'prop', 'p_numero'),
    ('Interior', 12, 'prop', 'p_interior'),
    ('Mz', 6, 'prop', 'p_mz'),
    ('Lt', 6, 'prop', 'p_lt'),
    ('Urbanización', 24, 'prop', 'p_urbanizacion'),
    ('Referencia', 24, 'prop', 'p_referencia'),
    ('Ubigeo', 9, 'prop', 'p_ubigeo'),
    ('Flags', 26, 'prop', 'p_flags'),
    ('Tipo de vía', 13, 'edit', 'p_tipo_via'),
    ('Nombre de vía', 24, 'edit', 'p_nombre_via'),
    ('Número', 8, 'edit', 'p_numero'),
    ('Interior', 12, 'edit', 'p_interior'),
    ('Mz', 6, 'edit', 'p_mz'),
    ('Lt', 6, 'edit', 'p_lt'),
    ('Urbanización', 24, 'edit', 'p_urbanizacion'),
    ('Ubigeo', 9, 'edit', 'p_ubigeo'),
    ('Estado', 16, 'edit', None),
    ('¿Difiere de la propuesta?', 11, 'meta', None),
    ('Comentario', 30, 'edit', None),
    ('Revisado por', 14, 'edit', None),
    ('Partición', 9, 'meta', None),
]
groups = {'src': ('DATOS DE EMS-D (no editar)', fill_h_src, fill_src),
          'prop': ('PROPUESTA DEL NORMALIZADOR (no editar)', fill_h_prop, fill_prop),
          'edit': ('ESPERADO (editar aquí)', fill_h_edit, fill_edit),
          'meta': ('CONTROL', fill_h_meta, None)}

# Fila 1: grupos; fila 2: encabezados.
from openpyxl.utils import get_column_letter as L
spans = []
for i, (_, _, g, _) in enumerate(cols, start=1):
    if spans and spans[-1][0] == g:
        spans[-1][2] = i
    else:
        spans.append([g, i, i])
for g, a, b in spans:
    label, hf, _ = groups[g]
    ws.cell(row=1, column=a, value=label if g != 'meta' else 'CONTROL')
    if b > a:
        ws.merge_cells(start_row=1, start_column=a, end_row=1, end_column=b)
    for c in range(a, b + 1):
        cell = ws.cell(row=1, column=c)
        cell.fill = hf; cell.font = white_bold; cell.alignment = Alignment(horizontal='center')
for i, (head, width, g, _) in enumerate(cols, start=1):
    c = ws.cell(row=2, column=i, value=head)
    c.font = white_bold; c.fill = groups[g][1]; c.alignment = Alignment(wrap_text=True, vertical='center'); c.border = border
    ws.column_dimensions[L(i)].width = width
ws.row_dimensions[2].height = 30

idx = {k: i for i, (_, _, _, k) in enumerate(cols, start=1) if k}
col_of = {head_g: i for i, head_g in enumerate([(c[0], c[2]) for c in cols], start=1)}
def C(head, g):
    return L(col_of[(head, g)])

ESTADO = col_of[('Estado', 'edit')]
DIF = col_of[('¿Difiere de la propuesta?', 'meta')]
PART = col_of[('Partición', 'meta')]
pairs = [('Tipo de vía',), ('Nombre de vía',), ('Número',), ('Interior',), ('Mz',), ('Lt',), ('Urbanización',), ('Ubigeo',)]

first = 3
for n, r in enumerate(chosen):
    row = first + n
    for i, (head, _, g, key) in enumerate(cols, start=1):
        if key is None:
            continue
        v = r[key]
        if key == 'cuenta':
            v = v[-6:]
        cell = ws.cell(row=row, column=i, value=v)
        cell.number_format = '@'
    ws.cell(row=row, column=ESTADO, value='PENDIENTE')
    conds = ','.join(f'EXACT({C(p[0], "edit")}{row},{C(p[0], "prop")}{row})' for p in pairs)
    ws.cell(row=row, column=DIF, value=f'=IF(AND({conds}),"No","Sí")')
    ws.cell(row=row, column=PART, value='test' if int(h('split' + r['id_muestra']), 16) % 100 < TEST_PCT else 'dev')
    for i, (_, _, g, _) in enumerate(cols, start=1):
        cell = ws.cell(row=row, column=i)
        cell.font = font; cell.alignment = wrap; cell.border = border
        if groups[g][2] is not None:
            cell.fill = groups[g][2]
last = first + len(chosen) - 1

ws.freeze_panes = ws.cell(row=first, column=4)
ws.auto_filter.ref = f'A2:{L(len(cols))}{last}'

dv_estado = DataValidation(type='list', formula1='"PENDIENTE,CORRECTO,CORREGIDO,NO ES DIRECCIÓN,DUDOSO"', allow_blank=False)
dv_estado.error = 'Elige un estado de la lista.'; dv_estado.errorTitle = 'Estado'
ws.add_data_validation(dv_estado); dv_estado.add(f'{L(ESTADO)}{first}:{L(ESTADO)}{last}')
types = 'AVENIDA,JIRON,CALLE,PASAJE,ALAMEDA,MALECON,PROLONGACION,CARRETERA,AUTOPISTA,OVALO,PLAZA,PARQUE,PASEO,CAMINO'
dv_tipo = DataValidation(type='list', formula1=f'"{types}"', allow_blank=True)
dv_tipo.error = 'Usa un tipo canónico o deja la celda vacía.'; dv_tipo.errorTitle = 'Tipo de vía'
ws.add_data_validation(dv_tipo); dv_tipo.add(f'{C("Tipo de vía", "edit")}{first}:{C("Tipo de vía", "edit")}{last}')

# Resalta en naranja las celdas esperadas que difieren de la propuesta.
orange = PatternFill('solid', start_color='F8CBAD')
for p in pairs:
    e, pr = C(p[0], 'edit'), C(p[0], 'prop')
    ws.conditional_formatting.add(f'{e}{first}:{e}{last}',
        FormulaRule(formula=[f'NOT(EXACT({e}{first},{pr}{first}))'], fill=orange))
ws[f'{L(ESTADO)}2'].comment = Comment('PENDIENTE: sin revisar. CORRECTO: la propuesta está bien. CORREGIDO: corregiste algún campo esperado. NO ES DIRECCIÓN: texto sin dirección utilizable. DUDOSO: no se puede decidir sin más información.', 'address-service')
ws[f'{L(DIF)}2'].comment = Comment('Fórmula: "Sí" si algún campo esperado es distinto de la propuesta (distingue mayúsculas).', 'address-service')
ws[f'{L(PART)}2'].comment = Comment('test = partición congelada para medir; dev = para ajustar reglas. Se asigna por hash del ID; no editar.', 'address-service')

# --- hoja Instrucciones ---
ins.column_dimensions['A'].width = 26
ins.column_dimensions['B'].width = 95
ins['A1'] = 'Etiquetado de la muestra real de EMS-D (pista C)'; ins['A1'].font = title
lines = [
    ('Qué es', f'{len(chosen)} direcciones reales de Lima y Callao tomadas de EMS-D. Para cada una, el normalizador propone cómo partirla. Tu trabajo es dejar en las columnas amarillas lo que es correcto.'),
    ('Confidencial', 'Contiene direcciones de clientes (Ley 29733). No lo envíes fuera del equipo ni lo subas al repositorio. Vive en data/private/.'),
    ('Qué editar', 'Solo las columnas amarillas de la hoja Etiquetado: ESPERADO (tipo de vía a Ubigeo), Estado, Comentario y Revisado por. Las columnas azules (EMS-D) y grises (propuesta) no se tocan.'),
    ('Cómo', '1) Lee la dirección original. 2) Compara con la propuesta. 3) Si está bien, pon Estado = CORRECTO. 4) Si algo está mal, corrige la celda amarilla (se pone naranja) y pon Estado = CORREGIDO. 5) Si no es una dirección, NO ES DIRECCIÓN. 6) Si no puedes decidir, DUDOSO y explica en Comentario.'),
    ('Convenciones', 'MAYÚSCULAS, sin tildes, la Ñ se conserva. Sin puntos ni comas. Tipo de vía canónico (AVENIDA, JIRON, CALLE, PASAJE…; vacío si el texto no lo dice: no lo inventes). Número: solo dígitos y, si existe, letra pegada (245A). Interior: con su marcador (DPTO 302, PISO 2, INT 4, OF 501, TDA 3). Mz y Lt: solo el valor (B, 12). Urbanización: con su marcador canónico (URBANIZACION, ASENTAMIENTO HUMANO, ASOCIACION, COOPERATIVA, RESIDENCIAL, CONDOMINIO, SECTOR…). Ubigeo: 6 dígitos del distrito donde está la dirección.'),
    ('No corrijas nombres', 'Si el cliente escribió "Saucez", el nombre esperado es SAUCEZ. Corregir la ortografía es trabajo de la resolución, no del parseo.'),
    ('Ubigeo', 'Por defecto es el que propone el normalizador. Cámbialo solo si sabes que la dirección está en otro distrito; explica por qué en Comentario.'),
    ('Partición', 'Las filas test se usan para medir la exactitud real y no deben usarse para ajustar reglas. Etiquétalas igual que las dev.'),
]
r0 = 3
for i, (a, b) in enumerate(lines):
    ins.cell(row=r0 + i, column=1, value=a).font = bold
    c = ins.cell(row=r0 + i, column=2, value=b); c.font = font; c.alignment = wrap
    ins.cell(row=r0 + i, column=1).alignment = Alignment(vertical='top')

ex = r0 + len(lines) + 1
ins.cell(row=ex, column=1, value='Ejemplo (inventado)').font = bold
example = [
    ('Dirección (EMS-D)', 'Jr Las Gardenias 205 dpto 302 urb los rosales - surco'),
    ('Propuesta', 'JIRON | LAS GARDENIAS | 205 | DPTO 302 | | | URBANIZACION LOS ROSALES | 150140'),
    ('Esperado', 'Igual a la propuesta → Estado = CORRECTO'),
    ('Dirección (EMS-D)', 'Los Laureles 610 204 San Borja'),
    ('Propuesta', ' | LOS LAURELES | 610 | 204 | | | | 150130'),
    ('Esperado', 'Interior = DPTO 204 (si sabes que es un departamento) → Estado = CORREGIDO'),
]
for i, (a, b) in enumerate(example):
    ins.cell(row=ex + 1 + i, column=1, value=a).font = font
    c = ins.cell(row=ex + 1 + i, column=2, value=b); c.font = font; c.alignment = wrap
leg = ex + len(example) + 2
ins.cell(row=leg, column=1, value='Colores').font = bold
for i, (fill, txt) in enumerate([(fill_src, 'Azul: datos originales de EMS-D'), (fill_prop, 'Gris: propuesta del normalizador'),
                                  (fill_edit, 'Amarillo: lo que editas'), (orange, 'Naranja: celda esperada que ya difiere de la propuesta')]):
    ins.cell(row=leg + 1 + i, column=1).fill = fill
    ins.cell(row=leg + 1 + i, column=2, value=txt).font = font
ins.cell(row=leg + 6, column=1, value='Fuente').font = bold
ins.cell(row=leg + 6, column=2, value='Muestra: scripts/emsd/muestra_direcciones.sql sobre bd_backoffice de producción (2026-10-01). Propuesta: go run ./cmd/sampleprofile (normalizer/0.6.0).').font = font

# --- hoja Resumen (fórmulas) ---
res.column_dimensions['A'].width = 34; res.column_dimensions['B'].width = 12; res.column_dimensions['C'].width = 12
res['A1'] = 'Avance del etiquetado'; res['A1'].font = title
rng = lambda col: f"Etiquetado!${L(col)}${first}:${L(col)}${last}"
res['A3'], res['B3'], res['C3'] = 'Estado', 'Filas', '% del total'
for c in ('A3', 'B3', 'C3'):
    res[c].font = white_bold; res[c].fill = fill_h_meta
states = ['PENDIENTE', 'CORRECTO', 'CORREGIDO', 'NO ES DIRECCIÓN', 'DUDOSO']
for i, st in enumerate(states):
    rr = 4 + i
    res.cell(row=rr, column=1, value=st).font = font
    res.cell(row=rr, column=2, value=f'=COUNTIF({rng(ESTADO)},A{rr})').font = font
    c = res.cell(row=rr, column=3, value=f'=IF($B$10=0,0,B{rr}/$B$10)'); c.font = font; c.number_format = '0.0%'
res['A10'] = 'Total'; res['A10'].font = bold
res['B10'] = f'=COUNTA(Etiquetado!$A${first}:$A${last})'; res['B10'].font = bold
res['A12'] = 'Revisadas (no PENDIENTE)'; res['A12'].font = font
res['B12'] = '=B10-B4'; res['B12'].font = font
res['A13'] = 'Propuesta correcta entre las revisadas'; res['A13'].font = font
res['B13'] = '=IF((B5+B6)=0,0,B5/(B5+B6))'; res['B13'].number_format = '0.0%'; res['B13'].font = font
res['C13'] = 'CORRECTO / (CORRECTO + CORREGIDO)'; res['C13'].font = Font(name=F, size=9, italic=True)
res['A14'] = 'Filas test'; res['A14'].font = font
res['B14'] = f'=COUNTIF({rng(PART)},"test")'; res['B14'].font = font

from openpyxl.workbook.properties import CalcProperties
wb.calculation = CalcProperties(fullCalcOnLoad=True)  # Excel calcula las fórmulas al abrir
wb.active = 1
wb.save(OUT)
print(OUT, len(chosen), 'filas', 'test=', sum(1 for r in chosen if int(h('split' + r['id_muestra']), 16) % 100 < TEST_PCT),
      'cuentas=', len({r['cuenta'] for r in chosen}))
