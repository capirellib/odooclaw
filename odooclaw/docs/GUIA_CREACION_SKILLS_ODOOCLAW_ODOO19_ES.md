# Guía para crear skills de OdooClaw integrados con Odoo 19

Esta guía define un procedimiento repetible para diseñar, desarrollar, probar y distribuir nuevas funciones de OdooClaw que lean o modifiquen información de Odoo 19.

Está dirigida a desarrolladores y administradores que mantienen una imagen Docker de OdooClaw para varios clientes. El ejemplo de referencia es el skill `bank-statement`, pero el mismo patrón sirve para ventas, compras, inventario, contabilidad, proyectos, recursos humanos y otras áreas de Odoo.

## 1. Idea principal

Un skill no se instala dentro de Odoo. Se ejecuta en la instancia de OdooClaw y usa el servidor `odoo-mcp` para comunicarse con Odoo mediante RPC.

```text
Usuario en Odoo Discuss
        |
        v
mail_bot_odooclaw (módulo Odoo 19)
        |
        v
Webhook HTTPS de OdooClaw
        |
        v
Skill: instrucciones y flujo de trabajo
        |
        v
Tool MCP: entrada validada
        |
        v
Servicio MCP: reglas de negocio
        |
        v
OdooClient / RPC
        |
        v
Modelos y permisos de Odoo 19
```

Las responsabilidades deben permanecer separadas:

| Capa | Responsabilidad |
| --- | --- |
| `SKILL.md` | Explicar cuándo actuar, qué herramientas usar y cuándo pedir confirmación. |
| Parser o script | Convertir archivos o entradas externas a datos normalizados; no escribir en Odoo. |
| Schema Pydantic | Validar tipos, límites y parámetros antes de ejecutar la operación. |
| Tool MCP | Exponer una interfaz pequeña, medible y documentada. |
| Servicio MCP | Validar permisos y registros, aplicar reglas de negocio y llamar a Odoo. |
| Odoo | Aplicar ACL, reglas de registro, restricciones contables y multiempresa. |

## 2. Ubicaciones que son fuente de verdad

En este repositorio, los archivos persistentes deben agregarse bajo:

```text
odooclaw/workspace/skills/
├── nombre-del-skill/
│   ├── SKILL.md
│   ├── references/
│   ├── scripts/
│   └── tests/
└── odoo-mcp/
    ├── requirements.txt
    ├── src/odoo_mcp/
    │   ├── schemas/
    │   ├── services/
    │   └── server.py
    └── tests/
```

No se debe desarrollar únicamente dentro de `/home/odooclaw/.odooclaw/workspace` de un contenedor en ejecución. Ese contenido puede desaparecer al recrear el contenedor y no se incorpora automáticamente a las próximas imágenes.

Después de modificar `odooclaw/workspace/`, se debe ejecutar `make generate` desde el directorio `odooclaw/`. OdooClaw copia ese workspace al paquete embebido durante la generación. No se debe editar directamente la copia generada en `cmd/odooclaw/internal/onboard/workspace/`.

## 3. Estructura mínima de un nuevo skill

Ejemplo para un skill llamado `customer-credit-review`:

```text
odooclaw/workspace/skills/customer-credit-review/
├── SKILL.md
├── references/
│   └── reglas-de-negocio.md
├── scripts/
│   └── normalize_input.py
└── tests/
    ├── fixtures/
    └── test_normalize_input.py
```

El encabezado de `SKILL.md` debe tener un nombre estable y una descripción que incluya los disparadores reales:

```yaml
---
name: customer-credit-review
description: Revisa crédito, deuda vencida y pedidos pendientes de clientes en Odoo 19. Usar cuando el usuario pregunte por riesgo de crédito, deuda del cliente o autorización de una venta.
---
```

El cuerpo debería contener, como mínimo:

1. Objetivo y límites.
2. Casos en los que se activa.
3. Tools MCP utilizadas.
4. Parámetros de entrada y respuesta.
5. Flujo de vista previa, confirmación y ejecución.
6. Permisos requeridos.
7. Errores esperados y recuperación.
8. Limitaciones conocidas.

## 4. Contrato entre el skill y `odoo-mcp`

El nombre, los campos y los ejemplos de `SKILL.md`, el schema, la tool y el servicio deben coincidir. Una divergencia entre `attachment_id` y `file_path`, por ejemplo, impide que el agente invoque correctamente la función.

Convención recomendada:

```python
class OperationSchema(BaseOdooRequest):
    record_id: int = Field(..., gt=0)
    dry_run: bool = Field(True)
    confirm: bool = Field(False)
```

```python
@mcp.tool()
def odoo_operation(
    record_id: int,
    dry_run: bool = True,
    confirm: bool = False,
    sender_id: int | None = None,
) -> dict:
    client = get_odoo_client()
    return operation(
        client=client,
        sender_id=sender_id or client.odoo_session.uid,
        record_id=record_id,
        dry_run=dry_run,
        confirm=confirm,
    )
```

Para operaciones que escriben información sensible, el contrato recomendado es:

- `dry_run=true`: analiza y devuelve el plan; no crea ni modifica registros.
- `dry_run=false` y `confirm=false`: rechaza la escritura e informa que falta confirmación.
- `dry_run=false` y `confirm=true`: vuelve a validar todo y ejecuta la operación.

No debe bastar con cambiar únicamente `dry_run` a `false` para crear asientos, pagos, movimientos de inventario o conciliaciones.

## 5. Archivos adjuntos y rutas seguras

No se recomienda exponer una ruta absoluta arbitraria, como `file_path`, directamente a una tool invocada por el modelo. Esto puede permitir la lectura accidental de archivos del contenedor.

Orden de preferencia:

1. Recibir un `attachment_id` de `ir.attachment` y descargarlo respetando el usuario y la empresa solicitante.
2. Recibir contenido codificado en base64 con nombre, tamaño y tipo MIME validados.
3. Aceptar rutas solamente dentro de un directorio de carga dedicado y comprobar la ruta resuelta antes de abrirla.

Validaciones mínimas:

- extensiones y MIME permitidos;
- tamaño máximo del archivo;
- cantidad máxima de filas, páginas y hojas;
- rechazo de rutas con escape del directorio permitido;
- tiempo máximo para OCR o procesos externos;
- eliminación segura de archivos temporales;
- no registrar contenido bancario, contraseñas ni tokens en logs.

## 6. Reglas específicas para Odoo 19

Antes de implementar una operación se deben inspeccionar los modelos y campos de la versión 19 instalada. No alcanza con copiar una implementación de Odoo 18.

Para extractos bancarios, el código base de Odoo 19 confirma lo siguiente:

- El modelo de cabecera es `account.bank.statement`.
- Las líneas son `account.bank.statement.line`.
- `account.bank.statement.line` delega en `account.move` mediante `_inherits = {'account.move': 'move_id'}`.
- Cada línea creada genera y publica su movimiento contable automáticamente.
- La etiqueta es `payment_ref`.
- `journal_id` es obligatorio en la línea.
- El `journal_id` de la cabecera es calculado a partir de `line_ids.journal_id`.
- La cabecera admite `balance_start` y `balance_end_real`; `balance_end` es calculado.

Por ello, una creación compatible debe colocar `journal_id` en cada línea:

```python
statement_values = {
    "name": reference,
    "reference": source_filename,
    "balance_start": opening_balance,
    "balance_end_real": closing_balance,
    "line_ids": [
        (0, 0, {
            "date": transaction["date"],
            "journal_id": journal_id,
            "payment_ref": transaction["description"] or "/",
            "amount": transaction["amount"],
            "partner_name": transaction.get("partner_hint") or "",
            "transaction_details": transaction.get("raw") or {},
        })
        for transaction in transactions
    ],
}
```

Además, antes de crear se debe comprobar que:

- el diario existe y su tipo es `bank` o `cash` según el caso;
- el diario pertenece a una empresa permitida para `sender_id`;
- todas las fechas y montos fueron validados;
- ninguna transacción ya fue importada;
- los saldos inicial, final y suma de movimientos son coherentes;
- el usuario técnico posee permisos suficientes sin usar `sudo` indiscriminadamente.

## 7. Parsers de documentos

Un parser debe devolver datos o errores explícitos. Nunca debe transformar silenciosamente un monto ilegible en cero.

Respuesta normalizada recomendada:

```json
{
  "format": "csv",
  "currency": "ARS",
  "opening_balance": "150000.00",
  "closing_balance": "142500.00",
  "transactions": [],
  "warnings": [],
  "errors": [],
  "source": {
    "filename": "extracto.csv",
    "sha256": "..."
  }
}
```

Para importes monetarios se recomienda `Decimal` y su serialización como texto hasta construir el payload RPC. Usar `float` desde el parser puede introducir diferencias de redondeo.

### CSV

- detectar `,`, `;`, tabulación y `|`;
- soportar UTF-8 y, cuando sea necesario, Windows-1252;
- reconocer `1.234,56`, `1,234.56`, paréntesis y signo final;
- validar que cada índice de columna exista en la fila;
- permitir configurar el sentido de débito/crédito según el banco;
- informar filas rechazadas con número de línea y causa.

### XLSX

- inspeccionar todas las hojas o permitir seleccionar una;
- detectar la fila real de encabezados;
- no crear un CSV temporal junto al archivo original;
- limitar cantidad de hojas, filas y celdas;
- usar `data_only=True` y advertir cuando una fórmula no tenga valor cacheado.

### OFX/QFX

- soportar OFX SGML 1.x y OFX XML 2.x;
- interpretar fechas `YYYYMMDDHHMMSS` con zona horaria;
- usar `FITID` como parte de la clave de deduplicación;
- extraer `CURDEF`, balances y fechas del período;
- rechazar transacciones sin fecha válida o sin importe válido.

### PDF

- `pdftotext` y `pypdf` solamente extraen texto; no son OCR;
- para documentos escaneados se necesita `pdf2image` más `pytesseract`, o pedir CSV/OFX;
- el resultado heurístico debe mostrarse siempre en vista previa;
- conservar número de página y texto original de cada línea para auditoría;
- definir límites de páginas, tiempo y resolución.

## 8. Duplicados e idempotencia

La misma solicitud puede repetirse por reintentos de red, del agente o del usuario. Toda operación de importación debe ser idempotente.

Para un extracto se recomienda calcular:

```text
source_hash = SHA-256 del archivo
transaction_key = empresa + diario + FITID
```

Cuando no exista `FITID`, utilizar una huella estable basada en empresa, diario, fecha, importe, referencia y descripción normalizada. La vista previa debe clasificar cada fila como `new`, `duplicate` o `invalid`.

Si el modelo estándar no ofrece un campo adecuado para guardar la clave, se necesita una decisión explícita: crear un pequeño módulo Odoo 19 con el campo y una restricción única, o mantener un registro de importaciones confiable fuera de Odoo. No se debe usar el texto visible como única protección.

## 9. Transacciones y fallos parciales

Una llamada RPC que crea una cabecera con sus líneas se ejecuta en una transacción de Odoo. Si una línea falla, la operación completa debería revertirse.

El servicio MCP debe:

1. validar el archivo completo antes de escribir;
2. hacer una única operación de creación cuando sea posible;
3. no reintentar escrituras sin clave de idempotencia;
4. devolver un error estructurado sin ocultar la causa;
5. comprobar el resultado mediante una lectura posterior de cabecera y líneas.

No se deben ejecutar `commit` o `rollback` remotos desde el MCP. La transacción pertenece a la solicitud RPC de Odoo.

## 10. Conciliación y automatizaciones posteriores

Importar un movimiento bancario y conciliarlo son operaciones diferentes.

Flujo seguro:

1. importar y verificar líneas;
2. obtener sugerencias con `odoo_suggest_bank_reconciliation`;
3. mostrar candidato, importe, fecha y confianza;
4. solicitar confirmación del usuario;
5. conciliar una línea mediante una tool específica;
6. verificar el estado posterior.

No se debe llamar a un método supuesto como `orm.make_cash_move_from_so` sin localizarlo en el código instalado, documentar su firma y probarlo en Odoo 19. Las ventas normalmente se consideran pagadas mediante facturas y pagos conciliados, no creando un movimiento de caja directamente desde el pedido.

## 11. Estrategia de pruebas

### Pruebas unitarias del parser

Fixtures mínimas:

- CSV separado por `;` con decimal argentino;
- CSV separado por `,` con decimal estadounidense;
- XLSX con varias hojas y encabezados desplazados;
- OFX SGML y OFX XML;
- PDF con texto y PDF escaneado;
- filas con fechas, importes y columnas inválidas;
- archivo vacío, demasiado grande o con extensión no permitida.

Comprobar fechas normalizadas, precisión decimal, signos, cantidad de filas, advertencias y errores.

### Pruebas del servicio MCP

Con un `OdooClient` simulado:

- `dry_run=true` no llama a `create`;
- falta de confirmación bloquea escrituras;
- diario inexistente o de otra empresa produce error;
- el payload contiene `journal_id` en cada línea;
- una falla RPC se comunica sin reportar éxito;
- una lectura posterior confirma cabecera y número de líneas;
- una repetición del mismo archivo no duplica movimientos.

### Prueba real con Odoo 19

Usar una base de pruebas aislada:

1. crear un diario bancario de pruebas;
2. importar un fixture pequeño;
3. comprobar `account.bank.statement`;
4. comprobar los `account.bank.statement.line` y sus `move_id` publicados;
5. comprobar saldos y empresa;
6. ejecutar sugerencias de conciliación;
7. conciliar solamente después de confirmación;
8. eliminar la base de pruebas al finalizar, no registros individuales de producción.

## 12. Dependencias

Las dependencias Python pertenecen a `odooclaw/workspace/skills/odoo-mcp/requirements.txt` o a un archivo de requisitos propio instalado explícitamente por el Dockerfile.

Para el ejemplo bancario ya existen `openpyxl`, `pypdf`, `pdf2image`, `pytesseract` y `requests`. Para OCR también se necesita instalar el binario del sistema `tesseract-ocr`; `pdf2image` normalmente necesita Poppler. Declarar una librería Python no instala sus binarios del sistema.

No se deben ignorar fallos de instalación con `pip install ... || true` en una imagen de producción: puede producir una imagen que arranca pero carece de funciones prometidas.

## 13. Integración en la imagen Docker

El procedimiento para distribuir el skill a todos los clientes es:

1. agregar el skill bajo `odooclaw/workspace/skills/`;
2. integrar schema, servicio, tool y tests en `odoo-mcp`;
3. declarar dependencias Python y del sistema;
4. ejecutar desde `odooclaw/`:

   ```bash
   make generate
   make test
   make vet
   ```

5. ejecutar las pruebas Python de `odoo-mcp`;
6. construir una etiqueta inmutable:

   ```bash
   docker build -t bettaerp/odooclaw:1.1.0 .
   docker push bettaerp/odooclaw:1.1.0
   ```

7. desplegar primero en una instancia de staging;
8. verificar salud, descubrimiento de la tool, vista previa y escritura controlada;
9. promover exactamente la misma etiqueta o digest a clientes;
10. conservar la versión anterior para rollback.

No conviene depender solamente de `latest`. Cada cliente debe quedar fijado a una etiqueta o digest verificable.

## 14. Rollback

Si una imagen nueva falla:

1. no borrar el volumen persistente;
2. detener la instancia afectada;
3. restaurar la etiqueta o digest anterior en Compose;
4. desplegar nuevamente;
5. verificar `/health` y los logs;
6. comprobar que Odoo conserva el mismo webhook;
7. registrar el archivo, tool y operación que causaron el fallo.

Los movimientos contables creados no deben borrarse automáticamente durante el rollback de la imagen. Su reversión requiere un procedimiento contable separado y autorizado.

## 15. Checklist para aprobar un nuevo skill

### Diseño

- [ ] El objetivo, alcance y disparadores están documentados.
- [ ] El skill vive en `odooclaw/workspace/skills/`.
- [ ] Existe separación entre parser, tool y servicio.
- [ ] El contrato de entrada coincide en todas las capas.

### Seguridad

- [ ] Respeta `sender_id`, empresas permitidas, ACL y reglas de registro.
- [ ] No usa `sudo` como solución general.
- [ ] No acepta rutas arbitrarias ni registra secretos.
- [ ] Tiene límites de tamaño, filas y tiempo.
- [ ] Las escrituras sensibles requieren confirmación explícita.

### Odoo 19

- [ ] Los modelos, campos y métodos existen en la versión instalada.
- [ ] Se probaron campos calculados, requeridos y heredados.
- [ ] La operación respeta multiempresa y moneda del diario.
- [ ] No se copiaron APIs obsoletas de Odoo 18.

### Calidad

- [ ] Hay pruebas unitarias y fixtures.
- [ ] `dry_run` demuestra que no hay escrituras.
- [ ] Hay deduplicación e idempotencia.
- [ ] Los errores son estructurados y auditables.
- [ ] Se verificó el resultado después de la escritura.

### Distribución

- [ ] Se ejecutó `make generate`.
- [ ] Pasaron pruebas Go y Python.
- [ ] La imagen tiene etiqueta inmutable.
- [ ] Se probó en staging.
- [ ] El rollback fue documentado.

## 16. Estado auditado del paquete `bank-statement`

El paquete recuperado del contenedor demuestra el concepto, pero todavía no está listo para producción con Odoo 19.

Ya contiene:

- `SKILL.md`;
- parser para CSV, XLSX, OFX/QFX, PDF y TXT;
- schema `ImportBankStatementSchema`;
- servicio `import_bank_statement`;
- tool `odoo_import_bank_statement`;
- integración declarada con las tools de conciliación existentes.

Antes de incluirlo en la imagen faltan estas correcciones:

1. Cambiar toda la documentación de Odoo 18 a Odoo 19.
2. Unificar `attachment_id` frente a `file_path` y aplicar una entrada segura.
3. Agregar `confirm` obligatorio para la escritura real.
4. Validar el diario, su tipo, empresa y moneda.
5. Colocar `journal_id` en cada línea de Odoo 19.
6. Aplicar `balance_start`, `balance_end_real` y referencia externa cuando existan.
7. Reemplazar `float` por validación decimal y rechazar montos inválidos.
8. Corregir la lectura de fechas OFX compactas.
9. Leer múltiples hojas XLSX sin escribir un CSV junto al archivo original.
10. Implementar OCR real o documentar claramente que no está disponible.
11. Añadir límites, deduplicación e idempotencia.
12. Añadir fixtures y pruebas de parser, MCP y Odoo 19.
13. Verificar que la conciliación existente sea compatible con los métodos reales de Odoo 19.
14. Ejecutar `make generate`, construir una imagen versionada y validarla en staging.

Hasta completar esta lista se debe usar solamente `dry_run=true` con archivos de prueba y nunca conectar la importación a contabilidad productiva.

## 17. Plantilla de solicitud para futuros skills

```text
Necesito crear el skill <nombre> para OdooClaw y Odoo 19.

Objetivo de negocio:
<qué problema resuelve>

Usuarios y permisos:
<quién lo utilizará y con qué empresas>

Modelos de Odoo involucrados:
<modelos conocidos; deben verificarse en Odoo 19>

Entradas:
<mensaje, attachment_id, campos o archivo>

Salidas:
<respuesta esperada>

Escrituras permitidas:
<qué puede crear o modificar>

Confirmación:
Toda escritura sensible debe soportar dry_run=true y exigir confirm=true.

Casos de error:
<datos faltantes, permisos, duplicados, conflictos>

Pruebas:
<fixtures, pruebas unitarias y escenario real aislado>

Distribución:
Integrar en odooclaw/workspace/skills, ejecutar make generate, probar y construir una imagen Docker con etiqueta inmutable.
```

## 18. Regla de cierre

Un skill está terminado cuando su código fuente vive en el repositorio, el contrato coincide en todas sus capas, las pruebas pasan contra Odoo 19, la imagen se puede reproducir y existe una ruta de rollback. Que funcione dentro de un único contenedor no es suficiente para distribuirlo a otros clientes.
