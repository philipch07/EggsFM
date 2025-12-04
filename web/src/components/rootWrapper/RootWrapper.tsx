import { useContext } from 'react';
import { Link, Outlet } from 'react-router-dom';
import React from 'react';
import { CinemaModeContext } from '../../providers/CinemaModeProvider';

import '98.css';

const RootWrapper = () => {
  const { cinemaMode } = useContext(CinemaModeContext);
  const navbarEnabled = !cinemaMode;

  return (
    <div>
      <nav className='window'>
        <div className='p-2'>
          <Link to='/' className='contents'>
            <button className='text-lg cursor-pointer'>🥚 Eggs FM</button>
          </Link>
        </div>
      </nav>

      <main className={`${navbarEnabled && 'pt-12 md:pt-12'}`}>
        <Outlet />
      </main>

      <footer className='mx-auto px-2 container py-6'>
        <ul className='flex items-center justify-center mt-3 text-sm:mt-0 space-x-4'>
          <li>
            <a
              href='https://github.com/Glimesh/broadcast-box'
              className='hover:underline'
            >
              GitHub
            </a>
          </li>
          <li>
            <a href='https://pion.ly' className='hover:underline'>
              Pion
            </a>
          </li>
          <li>
            <a href='https://glimesh.tv' className='hover:underline'>
              Glimesh
            </a>
          </li>
        </ul>
      </footer>
    </div>
  );
};

export default RootWrapper;
