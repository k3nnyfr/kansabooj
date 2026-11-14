// Swagger UI rendering of a stored spec. Loaded lazily (own chunk with its CSS): ~1.5 MB that most visits never need.
import type { ReactNode } from 'react'
import SwaggerUI from 'swagger-ui-react'
import 'swagger-ui-react/swagger-ui.css'
import './swagger.css'

// Swagger UI highlights code with a dark theme set in inline styles; render plain blocks themed by swagger.css.
const PlainCode = ({ children, className }: { children?: ReactNode; className?: string }) => (
  <pre className={`kb-code ${className ?? ''}`}>{children}</pre>
)
const plainCode = () => ({ components: { SyntaxHighlighter: PlainCode } })

// large: list only the tags (folded); otherwise list every operation (folded too).
export default function SwaggerView({ spec, large }: { spec: string; large: boolean }) {
  return (
    <div className="kb-swagger">
      <SwaggerUI
        spec={spec}
        plugins={[plainCode]}
        // Read-only: Kansabooj watches partner contracts, it must never call the APIs from the visitor's browser.
        supportedSubmitMethods={[]}
        tryItOutEnabled={false}
        requestSnippetsEnabled={false}
        docExpansion={large ? 'none' : 'list'}
        defaultModelsExpandDepth={0}
        deepLinking={false}
        filter
      />
    </div>
  )
}
