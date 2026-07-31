/**
 * Re-export shim for unified-gateway event parsing.
 *
 * The implementation moved to `@shared/codewhisperer/gateway-events` so the
 * CodeWhisperer protocol layer — reused by bundled plugin code — has no
 * `server/` imports. Host importers keep using this path unchanged.
 */
export {
	isGatewayEventType,
	parseGatewayDataEvent,
	parseGatewaySSEEvent,
} from "@shared/codewhisperer/gateway-events";
