import React, { useContext, useState } from 'react';
import Player from './Player';
import { useNavigate } from 'react-router-dom';
import { CinemaModeContext } from '../../providers/CinemaModeProvider';
import ModalTextInput from '../shared/ModalTextInput';

import '98.css';

const PlayerPage = () => {
  const navigate = useNavigate();
  const [streamKeys, setStreamKeys] = useState<string[]>([
    window.location.pathname.substring(1),
  ]);
  const [isModalOpen, setIsModelOpen] = useState<boolean>(false);

  const addStream = (streamKey: string) => {
    if (
      streamKeys.some(
        (key: string) => key.toLowerCase() === streamKey.toLowerCase(),
      )
    ) {
      return;
    }
    setStreamKeys((prev) => [...prev, streamKey]);
    setIsModelOpen((prev) => !prev);
  };

  return (
    <div>
      {isModalOpen && (
        <ModalTextInput<string>
          title='Add stream'
          message={'Insert stream key to add to multi stream'}
          isOpen={isModalOpen}
          canCloseOnBackgroundClick={false}
          onClose={() => setIsModelOpen(false)}
          onAccept={(result: string) => addStream(result)}
        />
      )}

      <div className={`flex flex-col w-full items-center`}>
        <div
          className={`grid ${streamKeys.length !== 1 ? 'grid-cols-2' : ''}  w-full gap-2`}
        >
          {streamKeys.map((streamKey) => (
            <Player
              key={`${streamKey}_player`}
              streamKey={streamKey}
              onCloseStream={
                streamKeys.length === 1
                  ? () => navigate('/')
                  : () =>
                      setStreamKeys((prev) =>
                        prev.filter((key) => key !== streamKey),
                      )
              }
            />
          ))}
        </div>
      </div>
    </div>
  );
};

export default PlayerPage;
