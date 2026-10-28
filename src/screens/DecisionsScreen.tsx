/** Decisions: obligations waiting on a person, read from the rig queue, oldest first. */

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { answerDecision, getDecisions, shortTime, type Decision } from "../lib/fleet";
import {
  Badge,
  Button,
  EmptyState,
  ErrorState,
  LoadingRows,
  Panel,
  PanelHead,
  Textarea,
  useToast,
} from "../components/ui";
import { PageHeader } from "../shell/PageHeader";

function deadline(decision: Decision): { label: string; overdue: boolean } | null {
  if (!decision.deadline_at) return null;
  const at = new Date(decision.deadline_at);
  if (Number.isNaN(at.getTime())) return null;
  const overdue = at.getTime() < Date.now();
  return { label: overdue ? `overdue ${shortTime(decision.deadline_at)}` : `due ${shortTime(decision.deadline_at)}`, overdue };
}

function DecisionCard({ decision }: { decision: Decision }) {
  const [text, setText] = useState("");
  const [selections, setSelections] = useState<Record<string, string>>({});
  const queryClient = useQueryClient();
  const toast = useToast();
  const due = deadline(decision);
  const questions = decision.questions ?? [];
  const answered = questions.every((question) => selections[question.id]);
  const composed = questions
    .map((question) => `${question.question}: ${selections[question.id] ?? ""}`)
    .join("\n");
  const value = questions.length > 0 ? [composed, text.trim()].filter(Boolean).join("\n") : text.trim();
  const ready = questions.length > 0 ? answered : text.trim().length > 0;

  const answer = useMutation({
    mutationFn: (next: string) => answerDecision(decision.id, next),
    onSuccess: (result) => {
      toast.done("Decision recorded", `The rig row is ${result.state}; event ${result.event_id}.`);
      setText("");
      setSelections({});
      void queryClient.invalidateQueries({ queryKey: ["decisions"] });
      void queryClient.invalidateQueries({ queryKey: ["board"] });
    },
    onError: (error) => {
      toast.problem("The answer did not stick", error instanceof Error ? error.message : String(error));
    },
  });

  return (
    <Panel>
      <PanelHead
        title={decision.summary}
        count={decision.tier ?? "decision"}
        description={`asked of ${decision.destination} · ${shortTime(decision.created_at)}`}
        actions={
          due ? (
            <Badge tone={due.overdue ? "ink" : "tint"} title={decision.deadline_at}>
              {due.label}
            </Badge>
          ) : (
            <Badge tone="quiet">no deadline</Badge>
          )
        }
      />
      <div className="grid gap-3 px-3 py-3 lg:grid-cols-[1fr_18rem]">
        <div className="min-w-0">
          <p className="label text-ink-muted">Question / brief</p>
          <pre className="mt-1 max-h-56 overflow-auto border border-rule bg-ground/40 p-2 text-[0.6875rem] leading-relaxed whitespace-pre-wrap text-ink">
            {decision.body || decision.summary}
          </pre>
          {decision.evidence_ref ? (
            <p className="mt-2 text-[0.6875rem] text-ink-muted">
              evidence: <span className="font-mono text-ink">{decision.evidence_ref}</span>
            </p>
          ) : null}
        </div>
        <div className="flex flex-col gap-2">
          <p className="label text-ink-muted">Answer</p>
          {questions.length > 0 ? (
            <div className="flex flex-col gap-2">
              {questions.map((question) => (
                <fieldset key={question.id}>
                  <legend className="text-[0.6875rem] text-ink">{question.question}</legend>
                  <div className="mt-1 flex flex-wrap gap-1.5">
                    {question.options.map((option) => (
                      <Button
                        key={option.id}
                        size="sm"
                        variant={selections[question.id] === option.label ? "primary" : "outline"}
                        title={option.recommended ? "recommended" : undefined}
                        onClick={() =>
                          setSelections((previous) => ({ ...previous, [question.id]: option.label }))
                        }
                      >
                        {option.label}
                        {option.recommended ? " · recommended" : ""}
                      </Button>
                    ))}
                  </div>
                </fieldset>
              ))}
              <Textarea
                aria-label={`Additional note for ${decision.summary}`}
                placeholder="Additional note (optional)"
                value={text}
                onChange={(event) => setText(event.target.value)}
                rows={2}
              />
            </div>
          ) : (
            <Textarea
              aria-label={`Free-text answer for ${decision.summary}`}
              placeholder="Type the answer the rig should record"
              value={text}
              onChange={(event) => setText(event.target.value)}
              rows={3}
            />
          )}
          <Button
            size="sm"
            variant="outline"
            disabled={!ready}
            busy={answer.isPending}
            onClick={() => answer.mutate(value)}
          >
            Send answer
          </Button>
          <p className="text-[0.6875rem] text-ink-muted">
            Options come from the rig row&apos;s questions; when it has none, the answer is free text. It is written
            with <span className="font-mono">rig queue resolve</span>; this board keeps no decision table.
          </p>
        </div>
      </div>
    </Panel>
  );
}

export function DecisionsScreen() {
  const { data, error, isLoading, refetch } = useQuery({
    queryKey: ["decisions"],
    queryFn: getDecisions,
    refetchInterval: 15000,
  });

  return (
    <div className="flex flex-col">
      <PageHeader
        title="Decisions"
        count={data?.decisions.length}
        description="Every obligation waiting on a person, oldest first. Answering resolves the rig row and writes one event."
      />
      <div className="flex flex-col gap-3 px-3 py-3 sm:px-5">
        {isLoading ? (
          <Panel>
            <LoadingRows rows={3} />
          </Panel>
        ) : error ? (
          <Panel>
            <ErrorState title="Decisions did not load" error={error} onRetry={() => void refetch()} />
          </Panel>
        ) : !data || data.decisions.length === 0 ? (
          <Panel>
            <EmptyState
              title="Nothing needs you"
              body="No rig queue row is addressed to a person right now. The fleet is free to move."
            />
          </Panel>
        ) : (
          data.decisions.map((decision) => <DecisionCard key={decision.id} decision={decision} />)
        )}
      </div>
    </div>
  );
}
