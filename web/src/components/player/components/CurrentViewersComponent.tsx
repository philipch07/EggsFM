import React, { useContext, useEffect, useState } from 'react';
import { UsersIcon } from '@heroicons/react/20/solid';
import { StatusContext } from '../../../providers/StatusProvider';

interface CurrentViewersComponentProps {
  streamKey: string;
}

const CurrentViewersComponent = (props: CurrentViewersComponentProps) => {
  const { streamKey } = props;
  const { streamStatus, refreshStatus } = useContext(StatusContext);
  const [currentViewersCount, setCurrentViewersCount] = useState<number>(0);

  useEffect(() => {
    refreshStatus();
  }, []);

  useEffect(() => {
    if (!streamKey || !streamStatus) {
      return;
    }

    const sessions = streamStatus.filter(
      (session) => session.streamKey === streamKey,
    );

    if (sessions.length !== 0) {
      setCurrentViewersCount(() =>
        sessions.length !== 0 ? sessions[0].whepSessions.length : 0,
      );
    }
  }, [streamStatus]);

  return (
    <div className='status-bar-field'>
      <p>Listeners: {currentViewersCount}</p>
    </div>
  );
};

export default CurrentViewersComponent;
