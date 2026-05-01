/**
 * Re-export the typed sonner API as our `toast`. App-wide style is
 * applied on the <Toaster/> mounted in App.tsx; call sites just import
 * `toast` from here and call `toast.success(...)`, `toast.error(...)`.
 */
export { toast } from 'sonner';
export type { ExternalToast } from 'sonner';
