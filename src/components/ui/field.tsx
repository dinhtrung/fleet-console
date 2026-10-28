"use client"

import * as React from "react"
import { useId, useMemo } from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"

import { Label } from "@/components/ui/label"
import { Separator } from "@/components/ui/separator"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { Checkbox as CheckboxPrimitive } from "@/components/ui/checkbox"

function FieldSet({ className, ...props }: React.ComponentProps<"fieldset">) {
  return (
    <fieldset
      data-slot="field-set"
      className={cn(
        "flex flex-col gap-4 has-[>[data-slot=checkbox-group]]:gap-3 has-[>[data-slot=radio-group]]:gap-3",
        className
      )}
      {...props}
    />
  )
}

function FieldLegend({
  className,
  variant = "legend",
  ...props
}: React.ComponentProps<"legend"> & { variant?: "legend" | "label" }) {
  return (
    <legend
      data-slot="field-legend"
      data-variant={variant}
      className={cn(
        "mb-1.5 font-medium data-[variant=label]:text-sm data-[variant=legend]:text-lead",
        className
      )}
      {...props}
    />
  )
}

function FieldGroup({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="field-group"
      className={cn(
        "group/field-group @container/field-group flex w-full flex-col gap-5 data-[slot=checkbox-group]:gap-3 *:data-[slot=field-group]:gap-4",
        className
      )}
      {...props}
    />
  )
}

const fieldVariants = cva(
  "group/field flex w-full gap-2 data-[invalid=true]:text-destructive",
  {
    variants: {
      orientation: {
        vertical: "flex-col *:w-full [&>.sr-only]:w-auto",
        horizontal:
          "flex-row items-center has-[>[data-slot=field-content]]:items-start *:data-[slot=field-label]:flex-auto has-[>[data-slot=field-content]]:[&>[role=checkbox],[role=radio]]:mt-px",
        responsive:
          "flex-col *:w-full @md/field-group:flex-row @md/field-group:items-center @md/field-group:*:w-auto @md/field-group:has-[>[data-slot=field-content]]:items-start @md/field-group:*:data-[slot=field-label]:flex-auto [&>.sr-only]:w-auto @md/field-group:has-[>[data-slot=field-content]]:[&>[role=checkbox],[role=radio]]:mt-px",
      },
    },
    defaultVariants: {
      orientation: "vertical",
    },
  }
)

function Field({
  className,
  orientation = "vertical",
  ...props
}: React.ComponentProps<"div"> & VariantProps<typeof fieldVariants>) {
  return (
    <div
      role="group"
      data-slot="field"
      data-orientation={orientation}
      className={cn(fieldVariants({ orientation }), className)}
      {...props}
    />
  )
}

function FieldContent({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="field-content"
      className={cn(
        "group/field-content flex flex-1 flex-col gap-0.5 leading-snug",
        className
      )}
      {...props}
    />
  )
}

function FieldLabel({
  className,
  ...props
}: React.ComponentProps<typeof Label>) {
  return (
    <Label
      data-slot="field-label"
      className={cn(
        "group/field-label peer/field-label flex w-fit gap-2 leading-snug group-data-[disabled=true]/field:opacity-50 has-data-checked:border-primary/30 has-data-checked:bg-primary/5 has-[>[data-slot=field]]:rounded-chip has-[>[data-slot=field]]:border has-[>[data-slot=field]]:not-has-[:disabled,[data-disabled]]:hover:bg-muted/50 has-[>[data-slot=field]]:has-[:focus-visible]:border-ring has-[>[data-slot=field]]:has-[:focus-visible]:ring-3 has-[>[data-slot=field]]:has-[:focus-visible]:ring-ring/50 *:data-[slot=field]:p-2.5  ",
        "has-[>[data-slot=field]]:w-full has-[>[data-slot=field]]:flex-col",
        className
      )}
      {...props}
    />
  )
}

function FieldTitle({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="field-label"
      className={cn(
        "flex w-fit items-center gap-2 text-sm font-medium group-data-[disabled=true]/field:opacity-50",
        className
      )}
      {...props}
    />
  )
}

function FieldDescription({ className, ...props }: React.ComponentProps<"p">) {
  return (
    <p
      data-slot="field-description"
      className={cn(
        "text-left text-sm leading-normal font-normal text-muted-foreground group-has-data-horizontal/field:text-balance [[data-variant=legend]+&]:-mt-1.5",
        "last:mt-0 nth-last-2:-mt-1",
        "[&>a]:underline [&>a]:underline-offset-4 [&>a:hover]:text-primary",
        className
      )}
      {...props}
    />
  )
}

function FieldSeparator({
  children,
  className,
  ...props
}: React.ComponentProps<"div"> & {
  children?: React.ReactNode
}) {
  return (
    <div
      data-slot="field-separator"
      data-content={!!children}
      className={cn(
        "relative -my-2 h-5 text-sm group-data-[variant=outline]/field-group:-mb-2",
        className
      )}
      {...props}
    >
      <Separator className="absolute inset-0 top-1/2" />
      {children && (
        <span
          className="relative mx-auto block w-fit bg-background px-2 text-muted-foreground"
          data-slot="field-separator-content"
        >
          {children}
        </span>
      )}
    </div>
  )
}

function FieldError({
  className,
  children,
  errors,
  ...props
}: React.ComponentProps<"div"> & {
  errors?: Array<{ message?: string } | undefined>
}) {
  const content = useMemo(() => {
    if (children) {
      return children
    }

    if (!errors?.length) {
      return null
    }

    const uniqueErrors = [
      ...new Map(errors.map((error) => [error?.message, error])).values(),
    ]

    if (uniqueErrors?.length == 1) {
      return uniqueErrors[0]?.message
    }

    return (
      <ul className="ml-4 flex list-disc flex-col gap-1">
        {uniqueErrors.map(
          (error, index) =>
            error?.message && <li key={index}>{error.message}</li>
        )}
      </ul>
    )
  }, [children, errors])

  if (!content) {
    return null
  }

  return (
    <div
      role="alert"
      data-slot="field-error"
      className={cn("text-sm font-normal text-destructive", className)}
      {...props}
    >
      {content}
    </div>
  )
}

/* ------------------------------------------------------------------ app controls
 *
 *  The board's form controls are the shadcn Field composition: `Field` groups the label, the control,
 *  the hint and the error, and `FieldError` owns the alert. These helpers keep the screens' existing
 *  `label`/`hint`/`error` API while every visual and semantic part comes from the registry file. */

const CONTROL =
  "min-h-[var(--density-control-h)] w-full rounded-chip border border-rule-strong bg-panel px-2 text-body text-ink " +
  "placeholder:text-ink-muted/70 disabled:cursor-not-allowed disabled:opacity-50";

function FieldShell({
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
  children: React.ReactNode;
  className?: string;
  optional?: boolean;
}) {
  return (
    <Field data-invalid={error ? true : undefined} className={className}>
      <FieldLabel htmlFor={htmlFor} className="label text-ink-muted">
        {label}
        {optional ? (
          <span className="ml-1 font-normal normal-case tracking-normal">(optional)</span>
        ) : null}
      </FieldLabel>
      {children}
      {hint && !error ? (
        <FieldDescription id={`${htmlFor}-hint`} className="text-[0.6875rem] text-ink-muted">
          {hint}
        </FieldDescription>
      ) : null}
      {error ? (
        <FieldError id={`${htmlFor}-error`} className="text-[0.6875rem] font-semibold text-attention">
          {error}
        </FieldError>
      ) : null}
    </Field>
  );
}

export interface TextInputProps extends React.ComponentProps<"input"> {
  label: string;
  hint?: string;
  error?: string;
}

export const TextInput = React.forwardRef<HTMLInputElement, TextInputProps>(function TextInput(
  { label, hint, error, className, id, ...rest },
  ref,
) {
  const generated = useId();
  const inputId = id ?? generated;
  return (
    <FieldShell label={label} hint={hint} error={error} htmlFor={inputId}>
      <Input
        ref={ref}
        id={inputId}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${inputId}-error` : hint ? `${inputId}-hint` : undefined}
        className={cn(CONTROL, error && "border-attention", className)}
        {...rest}
      />
    </FieldShell>
  );
});

export interface SelectInputProps extends React.ComponentProps<"select"> {
  label: string;
  hint?: string;
  error?: string;
  options: { value: string; label: string }[];
}

/** A native select: the board's option lists are short, closed sets, and the platform control keeps
 *  form submission and type-ahead. It still sits inside the shadcn Field that owns label and error. */
export const SelectInput = React.forwardRef<HTMLSelectElement, SelectInputProps>(function SelectInput(
  { label, hint, error, options, className, id, ...rest },
  ref,
) {
  const generated = useId();
  const selectId = id ?? generated;
  return (
    <FieldShell label={label} hint={hint} error={error} htmlFor={selectId}>
      <select
        ref={ref}
        id={selectId}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${selectId}-error` : hint ? `${selectId}-hint` : undefined}
        className={cn(CONTROL, "appearance-none py-1", error && "border-attention", className)}
        {...rest}
      >
        {options.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </select>
    </FieldShell>
  );
});

export interface TextAreaProps extends React.ComponentProps<"textarea"> {
  label: string;
  hint?: string;
  error?: string;
}

export const TextArea = React.forwardRef<HTMLTextAreaElement, TextAreaProps>(function TextArea(
  { label, hint, error, className, id, ...rest },
  ref,
) {
  const generated = useId();
  const areaId = id ?? generated;
  return (
    <FieldShell label={label} hint={hint} error={error} htmlFor={areaId}>
      <Textarea
        ref={ref}
        id={areaId}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? `${areaId}-error` : hint ? `${areaId}-hint` : undefined}
        className={cn(CONTROL, "min-h-20 py-1.5 leading-snug", error && "border-attention", className)}
        {...rest}
      />
    </FieldShell>
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
    <Field orientation="horizontal" className="items-start">
      <CheckboxPrimitive
        id={boxId}
        checked={checked}
        onCheckedChange={(next) => onChange(next)}
        disabled={disabled}
        className="mt-0.5"
      />
      <FieldContent>
        <FieldLabel htmlFor={boxId} className="text-body leading-snug">
          {label}
          {hint ? (
            <span className="block text-[0.6875rem] text-ink-muted">{hint}</span>
          ) : null}
        </FieldLabel>
      </FieldContent>
    </Field>
  );
}

export {
  Field,
  FieldLabel,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLegend,
  FieldSeparator,
  FieldSet,
  FieldContent,
  FieldTitle,
}
