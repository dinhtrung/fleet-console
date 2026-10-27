/** Form primitives. Every control: visible label, hint, and an error that is associated with the
 *  input (not just painted near it) and announced. */

import { forwardRef, useId } from "react";
import type { InputHTMLAttributes, ReactNode, SelectHTMLAttributes, TextareaHTMLAttributes } from "react";
import { ChevronDown } from "lucide-react";
import { IconCheck } from "@tabler/icons-react";
import { cn } from "../../lib/cn";

const CONTROL =
  "w-full min-h-[var(--density-control-h)] rounded-chip border border-rule-strong bg-panel px-2 text-body text-ink " +
  "placeholder:text-ink-muted/70 disabled:cursor-not-allowed disabled:opacity-50";

export function Field({
  label,
  hint,
  error,
  htmlFor,
  children,
  className,
  optional = false,
}: {
  label: string;
  hint?: string;
  error?: string;
  htmlFor: string;
  children: ReactNode;
  className?: string;
  optional?: boolean;
}) {
  return (
    <div className={cn("flex flex-col gap-1", className)}>
      <label htmlFor={htmlFor} className="label text-ink-muted">
        {label}
        {optional ? <span className="ml-1 font-normal normal-case tracking-normal">(optional)</span> : null}
      </label>
      {children}
      {hint && !error ? (
        <p id={`${htmlFor}-hint`} className="text-[0.6875rem] text-ink-muted">
          {hint}
        </p>
      ) : null}
      {error ? (
        <p id={`${htmlFor}-error`} className="text-[0.6875rem] font-semibold text-attention">
          {error}
        </p>
      ) : null}
    </div>
  );
}

export interface TextInputProps extends InputHTMLAttributes<HTMLInputElement> {
  label: string;
  hint?: string;
  error?: string;
}

export const TextInput = forwardRef<HTMLInputElement, TextInputProps>(function TextInput(
  { label, hint, error, className, id, ...rest },
  ref,
) {
  const generated = useId();
  const inputId = id ?? generated;
  return (
    <Field label={label} hint={hint} error={error} htmlFor={inputId}>
      <input
        ref={ref}
        id={inputId}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${inputId}-error` : hint ? `${inputId}-hint` : undefined}
        className={cn(CONTROL, error && "border-attention", className)}
        {...rest}
      />
    </Field>
  );
});

export interface SelectInputProps extends SelectHTMLAttributes<HTMLSelectElement> {
  label: string;
  hint?: string;
  error?: string;
  options: { value: string; label: string }[];
}

export const SelectInput = forwardRef<HTMLSelectElement, SelectInputProps>(function SelectInput(
  { label, hint, error, options, className, id, ...rest },
  ref,
) {
  const generated = useId();
  const selectId = id ?? generated;
  return (
    <Field label={label} hint={hint} error={error} htmlFor={selectId}>
      {/* The chevron is drawn as an element rather than baked into a background image: an inline SVG
          data-URI cannot inherit `currentColor`, so a hard-coded stroke left the only dropdown
          affordance at 1.39:1 against the dark panel. As an element it takes the theme's own token. */}
      <div className="relative">
        <select
          ref={ref}
          id={selectId}
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? `${selectId}-error` : hint ? `${selectId}-hint` : undefined}
          className={cn(CONTROL, "appearance-none pr-7", error && "border-attention", className)}
          {...rest}
        >
          {options.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </select>
        <ChevronDown
          size={12}
          aria-hidden="true"
          className="pointer-events-none absolute top-1/2 right-2 -translate-y-1/2 text-ink-muted"
        />
      </div>
    </Field>
  );
});

export interface TextAreaProps extends TextareaHTMLAttributes<HTMLTextAreaElement> {
  label: string;
  hint?: string;
  error?: string;
}

export const TextArea = forwardRef<HTMLTextAreaElement, TextAreaProps>(function TextArea(
  { label, hint, error, className, id, ...rest },
  ref,
) {
  const generated = useId();
  const areaId = id ?? generated;
  return (
    <Field label={label} hint={hint} error={error} htmlFor={areaId}>
      <textarea
        ref={ref}
        id={areaId}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${areaId}-error` : hint ? `${areaId}-hint` : undefined}
        className={cn(CONTROL, "min-h-20 py-1.5 leading-snug", error && "border-attention", className)}
        {...rest}
      />
    </Field>
  );
});

export function Checkbox({
  label,
  hint,
  checked,
  onChange,
  disabled,
  id,
}: {
  label: string;
  hint?: string;
  checked: boolean;
  onChange: (next: boolean) => void;
  disabled?: boolean;
  id?: string;
}) {
  const generated = useId();
  const boxId = id ?? generated;
  return (
    <div className="flex items-start gap-2">
      <span className="relative mt-0.5 inline-flex size-4 shrink-0">
        <input
          id={boxId}
          type="checkbox"
          checked={checked}
          disabled={disabled}
          onChange={(e) => onChange(e.target.checked)}
          className={cn(
            "peer size-4 appearance-none rounded-[2px] border border-rule-strong bg-panel",
            "checked:border-plum checked:bg-plum",
            "disabled:cursor-not-allowed disabled:opacity-50",
          )}
        />
        {/* A real glyph at the library's own weight, rather than a hand-written path in a data URI. */}
        <IconCheck
          size={11}
          stroke={2.5}
          aria-hidden="true"
          className="pointer-events-none absolute inset-0 m-auto text-on-plum opacity-0 peer-checked:opacity-100"
        />
      </span>
      <label htmlFor={boxId} className="text-body leading-snug">
        {label}
        {hint ? <span className="block text-[0.6875rem] text-ink-muted">{hint}</span> : null}
      </label>
    </div>
  );
}
